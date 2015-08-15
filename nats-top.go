// Copyright (c) 2015 NATS Messaging System
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"time"

	ui "github.com/gizak/termui"
	gnatsd "github.com/nats-io/gnatsd/server"
	. "github.com/nats-io/nats-top/util"
)

const version = "0.1.0"

var (
	host        = flag.String("s", "127.0.0.1", "The nats server host.")
	port        = flag.Int("m", 8222, "The nats server monitoring port.")
	conns       = flag.Int("n", 1024, "Maximum number of connections to poll.")
	delay       = flag.Int("d", 1, "Refresh interval in seconds.")
	sortBy      = flag.String("sort", "cid", "Value for which to sort by the connections.")
	showVersion = flag.Bool("v", false, "Show nats-top version")
)

func usage() {
	log.Fatalf("Usage: nats-top [-s server] [-m monitor_port] [-n num_connections] [-d delay_secs] [-sort by]\n")
}

func init() {
	log.SetFlags(0)
	flag.Usage = usage
	flag.Parse()
}

func main() {

	if *showVersion {
		log.Printf("nats-top v%s", version)
		os.Exit(0)
	}

	opts := map[string]interface{}{}
	opts["host"] = *host
	opts["port"] = *port
	opts["conns"] = *conns
	opts["delay"] = *delay

	if opts["host"] == nil || opts["port"] == nil {
		log.Fatalf("Please specify the monitoring port for NATS.\n")
		usage()
	}

	sortOpt := gnatsd.SortOpt(*sortBy)
	switch sortOpt {
	case SortByCid, SortBySubs, SortByOutMsgs, SortByInMsgs, SortByOutBytes, SortByInBytes:
		opts["sort"] = sortOpt
	default:
		log.Printf("nats-top: not a valid option to sort by: %s\n", sortOpt)
	}

	err := ui.Init()
	if err != nil {
		panic(err)
	}
	defer ui.Close()

	statsCh := make(chan *Stats)

	go monitorStats(opts, statsCh)

	StartUI(opts, statsCh)
}

// clearScreen tries to ensure resetting original state of screen
func clearScreen() {
	fmt.Print("\033[2J\033[1;1H\033[?25l")
}

func cleanExit() {
	clearScreen()
	ui.Close()

	// Show cursor once again
	fmt.Print("\033[?25h")
	os.Exit(0)
}

func exitWithError() {
	ui.Close()
	os.Exit(1)
}

// monitorStats can be ran as a goroutine and takes options
// which can modify how to do the polling
func monitorStats(
	opts map[string]interface{},
	statsCh chan *Stats,
) {
	var pollTime time.Time

	var inMsgsDelta int64
	var outMsgsDelta int64
	var inBytesDelta int64
	var outBytesDelta int64

	var inMsgsLastVal int64
	var outMsgsLastVal int64
	var inBytesLastVal int64
	var outBytesLastVal int64

	var inMsgsRate float64
	var outMsgsRate float64
	var inBytesRate float64
	var outBytesRate float64

	first := true
	pollTime = time.Now()

	for {
		// Note that delay defines the sampling rate as well
		if val, ok := opts["delay"].(int); ok {
			time.Sleep(time.Duration(val) * time.Second)
		} else {
			log.Fatalf("error: could not use %s as a refreshing interval", opts["delay"])
			break
		}

		// Wrap collected info in a Stats struct
		stats := &Stats{
			Varz:  &gnatsd.Varz{},
			Connz: &gnatsd.Connz{},
			Rates: &Rates{},
		}

		// Get /varz
		{
			result, err := Request("/varz", opts)
			if err != nil {
				fmt.Fprintf(os.Stderr, "could not get /varz: %v", err)
				statsCh <- stats
				continue
			}
			if varz, ok := result.(*gnatsd.Varz); ok {
				stats.Varz = varz
			}
		}

		// Get /connz
		{
			result, err := Request("/connz", opts)
			if err != nil {
				fmt.Fprintf(os.Stderr, "could not get /connz: %v", err)
				statsCh <- stats
				continue
			}

			if connz, ok := result.(*gnatsd.Connz); ok {
				stats.Connz = connz
			}
		}

		// Periodic snapshot to get per sec metrics
		inMsgsVal := stats.Varz.InMsgs
		outMsgsVal := stats.Varz.OutMsgs
		inBytesVal := stats.Varz.InBytes
		outBytesVal := stats.Varz.OutBytes

		inMsgsDelta = inMsgsVal - inMsgsLastVal
		outMsgsDelta = outMsgsVal - outMsgsLastVal
		inBytesDelta = inBytesVal - inBytesLastVal
		outBytesDelta = outBytesVal - outBytesLastVal

		inMsgsLastVal = inMsgsVal
		outMsgsLastVal = outMsgsVal
		inBytesLastVal = inBytesVal
		outBytesLastVal = outBytesVal

		now := time.Now()
		tdelta := now.Sub(pollTime)
		pollTime = now

		// Calculate rates but the first time
		if first {
			first = false
		} else {
			inMsgsRate = float64(inMsgsDelta) / tdelta.Seconds()
			outMsgsRate = float64(outMsgsDelta) / tdelta.Seconds()
			inBytesRate = float64(inBytesDelta) / tdelta.Seconds()
			outBytesRate = float64(outBytesDelta) / tdelta.Seconds()
		}

		stats.Rates = &Rates{
			InMsgsRate:   inMsgsRate,
			OutMsgsRate:  outMsgsRate,
			InBytesRate:  inBytesRate,
			OutBytesRate: outBytesRate,
		}

		// Send update
		statsCh <- stats
	}
}

// generateParagraph takes an options map and latest Stats
// then returns a formatted paragraph ready to be rendered
func generateParagraph(
	opts map[string]interface{},
	stats *Stats,
) string {

	// Snapshot current stats
	cpu := stats.Varz.CPU
	memVal := stats.Varz.Mem
	uptime := stats.Varz.Uptime
	numConns := stats.Connz.NumConns
	inMsgsVal := stats.Varz.InMsgs
	outMsgsVal := stats.Varz.OutMsgs
	inBytesVal := stats.Varz.InBytes
	outBytesVal := stats.Varz.OutBytes
	slowConsumers := stats.Varz.SlowConsumers

	var serverVersion string
	if stats.Varz.Info != nil {
		serverVersion = stats.Varz.Info.Version
	}

	mem := Psize(memVal)
	inMsgs := Psize(inMsgsVal)
	outMsgs := Psize(outMsgsVal)
	inBytes := Psize(inBytesVal)
	outBytes := Psize(outBytesVal)
	inMsgsRate := stats.Rates.InMsgsRate
	outMsgsRate := stats.Rates.OutMsgsRate
	inBytesRate := Psize(int64(stats.Rates.InBytesRate))
	outBytesRate := Psize(int64(stats.Rates.OutBytesRate))

	info := "gnatsd version %s (uptime: %s)"
	info += "\nServer:\n  Load: CPU:  %.1f%%  Memory: %s  Slow Consumers: %d\n"
	info += "  In:   Msgs: %s  Bytes: %s  Msgs/Sec: %.1f  Bytes/Sec: %s\n"
	info += "  Out:  Msgs: %s  Bytes: %s  Msgs/Sec: %.1f  Bytes/Sec: %s"

	text := fmt.Sprintf(info, serverVersion, uptime,
		cpu, mem, slowConsumers,
		inMsgs, inBytes, inMsgsRate, inBytesRate,
		outMsgs, outBytes, outMsgsRate, outBytesRate)
	text += fmt.Sprintf("\n\nConnections: %d\n", numConns)

	connHeader := "  %-20s %-8s %-6s  %-10s  %-10s  %-10s  %-10s  %-10s  %-7s  %-7s\n"

	connRows := fmt.Sprintf(connHeader, "HOST", "CID", "SUBS", "PENDING",
		"MSGS_TO", "MSGS_FROM", "BYTES_TO", "BYTES_FROM",
		"LANG", "VERSION")
	text += connRows
	connValues := "  %-20s %-8d %-6d  %-10d  %-10s  %-10s  %-10s  %-10s  %-7s  %-7s\n"

	switch opts["sort"] {
	case SortByCid:
		sort.Sort(ByCid(stats.Connz.Conns))
	case SortBySubs:
		sort.Sort(sort.Reverse(BySubs(stats.Connz.Conns)))
	case SortByOutMsgs:
		sort.Sort(sort.Reverse(ByMsgsTo(stats.Connz.Conns)))
	case SortByInMsgs:
		sort.Sort(sort.Reverse(ByMsgsFrom(stats.Connz.Conns)))
	case SortByOutBytes:
		sort.Sort(sort.Reverse(ByBytesTo(stats.Connz.Conns)))
	case SortByInBytes:
		sort.Sort(sort.Reverse(ByBytesFrom(stats.Connz.Conns)))
	}

	for _, conn := range stats.Connz.Conns {
		host := fmt.Sprintf("%s:%d", conn.IP, conn.Port)
		connLine := fmt.Sprintf(connValues, host, conn.Cid, conn.NumSubs, conn.Pending,
			Psize(conn.OutMsgs), Psize(conn.InMsgs), Psize(conn.OutBytes), Psize(conn.InBytes),
			conn.Lang, conn.Version)
		text += connLine
	}

	return text
}

// StartUI periodically refreshes the screen using recent data
func StartUI(
	opts map[string]interface{},
	statsCh chan *Stats,
) {

	cleanStats := &Stats{
		Varz:  &gnatsd.Varz{},
		Connz: &gnatsd.Connz{},
		Rates: &Rates{},
	}

	// Show empty values on first display
	text := generateParagraph(opts, cleanStats)
	par := ui.NewPar(text)
	par.Height = ui.TermHeight()
	par.Width = ui.TermWidth()
	par.HasBorder = false

	// cpu and conns share the same space in the grid so handled differently
	cpuChart := ui.NewGauge()
	cpuChart.Border.Label = "Cpu: "
	cpuChart.Height = ui.TermHeight() / 7
	cpuChart.BarColor = ui.ColorGreen
	cpuChart.PercentColor = ui.ColorBlue

	connsChart := ui.NewLineChart()
	connsChart.Border.Label = "Connections: "
	connsChart.Height = ui.TermHeight() / 5
	connsChart.Mode = "dot"
	connsChart.AxesColor = ui.ColorWhite
	connsChart.LineColor = ui.ColorYellow | ui.AttrBold
	connsChart.Data = []float64{0}

	// All other boxes of the same size
	boxHeight := ui.TermHeight() / 3

	memChart := ui.NewLineChart()
	memChart.Border.Label = "Memory: "
	memChart.Height = boxHeight
	memChart.Mode = "dot"
	memChart.AxesColor = ui.ColorWhite
	memChart.LineColor = ui.ColorYellow | ui.AttrBold
	memChart.Data = []float64{0.0}

	inMsgsChartLine := ui.Sparkline{}
	inMsgsChartLine.Height = boxHeight - boxHeight/7
	inMsgsChartLine.LineColor = ui.ColorCyan
	inMsgsChartLine.TitleColor = ui.ColorWhite
	inMsgsChartBox := ui.NewSparklines(inMsgsChartLine)
	inMsgsChartLine.Data = []int{0}
	inMsgsChartBox.Height = boxHeight
	inMsgsChartBox.Border.Label = "In: Msgs/Sec: "

	inBytesChartLine := ui.Sparkline{}
	inBytesChartLine.Height = boxHeight - boxHeight/7
	inBytesChartLine.LineColor = ui.ColorCyan
	inBytesChartLine.TitleColor = ui.ColorWhite
	inBytesChartLine.Data = []int{0}
	inBytesChartBox := ui.NewSparklines(inBytesChartLine)
	inBytesChartBox.Height = boxHeight
	inBytesChartBox.Border.Label = "In: Bytes/Sec: "

	outMsgsChartLine := ui.Sparkline{}
	outMsgsChartLine.Height = boxHeight - boxHeight/7
	outMsgsChartLine.LineColor = ui.ColorGreen
	outMsgsChartLine.TitleColor = ui.ColorWhite
	outMsgsChartLine.Data = []int{0}
	outMsgsChartBox := ui.NewSparklines(outMsgsChartLine)
	outMsgsChartBox.Height = boxHeight
	outMsgsChartBox.Border.Label = "Out: Msgs/Sec: "

	outBytesChartLine := ui.Sparkline{}
	outBytesChartLine.Height = boxHeight - boxHeight/7
	outBytesChartLine.LineColor = ui.ColorGreen
	outBytesChartLine.TitleColor = ui.ColorWhite
	outBytesChartLine.Data = []int{0}
	outBytesChartBox := ui.NewSparklines(outBytesChartLine)
	outBytesChartBox.Height = boxHeight
	outBytesChartBox.Border.Label = "Out: Bytes/Sec: "

	// Dashboard like view
	//
	// ....cpu.........  ...mem.........
	// .              .  .             .
	// .              .  .             .
	// ....conns.......  .             .
	// .              .  .             .
	// .              .  .             .
	// ................  ...............
	//
	// ..in msgs/sec...  ..in bytes/sec.
	// .              .  .             .
	// .              .  .             .
	// .              .  .             .
	// .              .  .             .
	// ................  ...............
	//
	// ..out msgs/sec..  .out bytes/sec.
	// .              .  .             .
	// .              .  .             .
	// .              .  .             .
	// .              .  .             .
	// ................  ...............
	//
	cpuMemConnsCharts := ui.NewRow(
		ui.NewCol(6, 0, cpuChart, connsChart),
		ui.NewCol(6, 0, memChart),
	)

	inCharts := ui.NewRow(
		ui.NewCol(6, 0, inMsgsChartBox),
		ui.NewCol(6, 0, inBytesChartBox),
	)

	outCharts := ui.NewRow(
		ui.NewCol(6, 0, outMsgsChartBox),
		ui.NewCol(6, 0, outBytesChartBox),
	)

	// Top like view
	//
	paraRow := ui.NewRow(ui.NewCol(ui.TermWidth(), 0, par))

	// Create grids that we'll be using to toggle what to render
	dashboardGrid := ui.NewGrid(cpuMemConnsCharts, inCharts, outCharts)
	topViewGrid := ui.NewGrid(paraRow)

	// Start with the topviewGrid by default
	ui.Body.Rows = topViewGrid.Rows
	ui.Body.Align()
	viewMode := "top"

	// Used for pinging the IU to refresh the screen with new values
	redraw := make(chan struct{})

	update := func() {
		for {
			stats := <-statsCh

			// Snapshot current stats
			cpu := stats.Varz.CPU
			memVal := stats.Varz.Mem
			numConns := stats.Connz.NumConns
			inMsgsRate := stats.Rates.InMsgsRate
			outMsgsRate := stats.Rates.OutMsgsRate
			inBytesRate := stats.Rates.InBytesRate
			outBytesRate := stats.Rates.OutBytesRate

			var maxConn int
			if stats.Varz.Options != nil {
				maxConn = stats.Varz.Options.MaxConn
			}

			// Update top view text
			text = generateParagraph(opts, stats)
			par.Text = text

			// Update dashboard components
			cpuChart.Border.Label = fmt.Sprintf("CPU: %.1f%% ", cpu)
			cpuChart.Percent = int(cpu)

			connsChart.Border.Label = fmt.Sprintf("Connections: %d/%d ", numConns, maxConn)
			connsChart.Data = append(connsChart.Data, float64(numConns))
			if len(connsChart.Data) > 150 {
				connsChart.Data = connsChart.Data[1:150]
			}

			memChart.Border.Label = fmt.Sprintf("Memory: %s", Psize(memVal))
			memChart.Data = append(memChart.Data, float64(memVal/1024/1024))
			if len(memChart.Data) > 150 {
				memChart.Data = memChart.Data[1:150]
			}

			inMsgsChartBox.Border.Label = fmt.Sprintf("In: Msgs/Sec: %.1f ", inMsgsRate)
			inMsgsChartBox.Lines[0].Data = append(inMsgsChartBox.Lines[0].Data, int(inMsgsRate))
			if len(inMsgsChartBox.Lines[0].Data) > 150 {
				inMsgsChartBox.Lines[0].Data = inMsgsChartBox.Lines[0].Data[1:150]
			}

			inBytesChartBox.Border.Label = fmt.Sprintf("In: Bytes/Sec: %s ", Psize(int64(inBytesRate)))
			inBytesChartBox.Lines[0].Data = append(inBytesChartBox.Lines[0].Data, int(inBytesRate))
			if len(inBytesChartBox.Lines[0].Data) > 150 {
				inBytesChartBox.Lines[0].Data = inBytesChartBox.Lines[0].Data[1:150]
			}

			outMsgsChartBox.Border.Label = fmt.Sprintf("Out: Msgs/Sec: %.1f ", outMsgsRate)
			outMsgsChartBox.Lines[0].Data = append(outMsgsChartBox.Lines[0].Data, int(outMsgsRate))
			if len(outMsgsChartBox.Lines[0].Data) > 150 {
				outMsgsChartBox.Lines[0].Data = outMsgsChartBox.Lines[0].Data[1:150]
			}

			outBytesChartBox.Border.Label = fmt.Sprintf("Out: Bytes/Sec: %s ", Psize(int64(outBytesRate)))
			outBytesChartBox.Lines[0].Data = append(outBytesChartBox.Lines[0].Data, int(outBytesRate))
			if len(outBytesChartBox.Lines[0].Data) > 150 {
				outBytesChartBox.Lines[0].Data = outBytesChartBox.Lines[0].Data[1:150]
			}

			redraw <- struct{}{}
		}
	}

	// Flags for capturing options
	waitingSortOption := false
	waitingLimitOption := false

	optionBuf := ""
	refreshOptionHeader := func() {
		// Need to mask what was typed before
		clrline := "\033[1;1H\033[6;1H                  "

		clrline += "  "
		for i := 0; i < len(optionBuf); i++ {
			clrline += "  "
		}
		fmt.Printf(clrline)
	}

	evt := ui.EventCh()

	ui.Render(ui.Body)

	go update()

	for {
		select {
		case e := <-evt:

			if waitingSortOption {

				if e.Type == ui.EventKey && e.Key == ui.KeyEnter {

					sortOpt := gnatsd.SortOpt(optionBuf)
					switch sortOpt {
					case SortByCid, SortBySubs, SortByOutMsgs, SortByInMsgs, SortByOutBytes, SortByInBytes:
						opts["sort"] = sortOpt
					default:
						go func() {
							// Has to be at least of the same length as sort by header
							emptyPadding := ""
							if len(optionBuf) < 5 {
								emptyPadding = "       "
							}
							fmt.Printf("\033[1;1H\033[6;1Hinvalid order: %s%s", emptyPadding, optionBuf)
							time.Sleep(1 * time.Second)
							waitingSortOption = false
							refreshOptionHeader()
							optionBuf = ""
						}()
						continue
					}

					refreshOptionHeader()
					waitingSortOption = false
					optionBuf = ""
					continue
				}

				// Handle backspace
				if e.Type == ui.EventKey && len(optionBuf) > 0 && (e.Key == ui.KeyBackspace || e.Key == ui.KeyBackspace2) {
					optionBuf = optionBuf[:len(optionBuf)-1]
					refreshOptionHeader()
				} else {
					optionBuf += string(e.Ch)
				}
				fmt.Printf("\033[1;1H\033[6;1Hsort by [%s]: %s", opts["sort"], optionBuf)
			}

			if waitingLimitOption {

				if e.Type == ui.EventKey && e.Key == ui.KeyEnter {

					var n int
					_, err := fmt.Sscanf(optionBuf, "%d", &n)
					if err == nil {
						opts["conns"] = n
					}

					waitingLimitOption = false
					optionBuf = ""
					refreshOptionHeader()
					continue
				}

				// Handle backspace
				if e.Type == ui.EventKey && len(optionBuf) > 0 && (e.Key == ui.KeyBackspace || e.Key == ui.KeyBackspace2) {
					optionBuf = optionBuf[:len(optionBuf)-1]
					refreshOptionHeader()
				} else {
					optionBuf += string(e.Ch)
				}
				fmt.Printf("\033[1;1H\033[6;1Hlimit   [%d]: %s", opts["conns"], optionBuf)
			}

			if e.Type == ui.EventKey && e.Ch == 'q' {
				cleanExit()
			}

			if e.Type == ui.EventKey && e.Ch == 'o' && !waitingLimitOption {
				fmt.Printf("\033[1;1H\033[6;1Hsort by [%s]:", opts["sort"])
				waitingSortOption = true
			}

			if e.Type == ui.EventKey && e.Ch == 'n' && !waitingSortOption {
				fmt.Printf("\033[1;1H\033[6;1Hlimit   [%d]:", opts["conns"])
				waitingLimitOption = true
			}

			if e.Type == ui.EventKey && e.Key == ui.KeySpace {

				// Toggle between one of the views
				switch viewMode {
				case "top":
					refreshOptionHeader()
					ui.Body.Rows = dashboardGrid.Rows
					viewMode = "dashboard"
					waitingSortOption = false
					waitingLimitOption = false
				case "dashboard":
					ui.Body.Rows = topViewGrid.Rows
					viewMode = "top"
				}
				ui.Body.Align()
			}

			if e.Type == ui.EventResize {

				switch viewMode {
				case "dashboard":
					ui.Body.Width = ui.TermWidth()

					// Refresh size of boxes accordingly
					cpuChart.Height = ui.TermHeight() / 7
					connsChart.Height = ui.TermHeight() / 5

					boxHeight := ui.TermHeight() / 3
					lineHeight := boxHeight - boxHeight/7

					memChart.Height = boxHeight

					inMsgsChartBox.Height = boxHeight
					inMsgsChartBox.Lines[0].Height = lineHeight

					outMsgsChartBox.Height = boxHeight
					outMsgsChartBox.Lines[0].Height = lineHeight

					inBytesChartBox.Height = boxHeight
					inBytesChartBox.Lines[0].Height = lineHeight

					outBytesChartBox.Height = boxHeight
					outBytesChartBox.Lines[0].Height = lineHeight
				}

				ui.Body.Align()
				go func() { redraw <- struct{}{} }()
			}

		case <-redraw:
			ui.Render(ui.Body)
		}
	}
}
