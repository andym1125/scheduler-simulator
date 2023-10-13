package main

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/olekukonko/tablewriter"
)

const TIME_QUANTUM int64 = 2

func main() {
	// CLI args
	f, closeFile, err := openProcessingFile(os.Args...)
	if err != nil {
		log.Fatal(err)
	}
	defer closeFile()

	// Load and parse processes
	processes, err := loadProcesses(f)
	if err != nil {
		log.Fatal(err)
	}

	// First-come, first-serve scheduling
	FCFSSchedule(os.Stdout, "First-come, first-serve", processes)

	// Shortest job first scheduling
	SJFSchedule(os.Stdout, "Shortest-job-first", processes)

	// Shortest job first with priority scheduling
	SJFPrioritySchedule(os.Stdout, "Priority", processes)

	// Round robin scheduling
	// Default time quantum is 2, but can be changed using the TIME_QUANTUM constant
	RRSchedule(os.Stdout, "Round-robin", processes)
}

func openProcessingFile(args ...string) (*os.File, func(), error) {
	if len(args) != 2 {
		return nil, nil, fmt.Errorf("%w: must give a scheduling file to process", ErrInvalidArgs)
	}
	// Read in CSV process CSV file
	f, err := os.Open(args[1])
	if err != nil {
		return nil, nil, fmt.Errorf("%v: error opening scheduling file", err)
	}
	closeFn := func() {
		if err := f.Close(); err != nil {
			log.Fatalf("%v: error closing scheduling file", err)
		}
	}

	return f, closeFn, nil
}

type (
	Process struct {
		ProcessID     int64
		ArrivalTime   int64
		BurstDuration int64
		Priority      int64
		BurstLeft     int64
		ExitTime      int64
	}
	TimeSlice struct {
		PID   int64
		Start int64
		Stop  int64
	}
)

//region Schedulers

// FCFSSchedule outputs a schedule of processes in a GANTT chart and a table of timing given:
// • an output writer
// • a title for the chart
// • a slice of processes
func FCFSSchedule(w io.Writer, title string, processes []Process) {
	var (
		serviceTime     int64
		totalWait       float64
		totalTurnaround float64
		lastCompletion  float64
		waitingTime     int64
		schedule        = make([][]string, len(processes))
		gantt           = make([]TimeSlice, 0)
	)
	for i := range processes {
		if processes[i].ArrivalTime > 0 {
			waitingTime = serviceTime - processes[i].ArrivalTime
		}
		totalWait += float64(waitingTime)

		start := waitingTime + processes[i].ArrivalTime

		turnaround := processes[i].BurstDuration + waitingTime
		totalTurnaround += float64(turnaround)

		completion := processes[i].BurstDuration + processes[i].ArrivalTime + waitingTime
		lastCompletion = float64(completion)

		schedule[i] = []string{
			fmt.Sprint(processes[i].ProcessID),
			fmt.Sprint(processes[i].Priority),
			fmt.Sprint(processes[i].BurstDuration),
			fmt.Sprint(processes[i].ArrivalTime),
			fmt.Sprint(waitingTime),
			fmt.Sprint(turnaround),
			fmt.Sprint(completion),
		}
		serviceTime += processes[i].BurstDuration

		gantt = append(gantt, TimeSlice{
			PID:   processes[i].ProcessID,
			Start: start,
			Stop:  serviceTime,
		})
	}

	count := float64(len(processes))
	aveWait := totalWait / count
	aveTurnaround := totalTurnaround / count
	aveThroughput := count / lastCompletion

	outputTitle(w, title)
	outputGantt(w, gantt)
	outputSchedule(w, schedule, aveWait, aveTurnaround, aveThroughput)
}

// SJFPrioritySchedule outputs a schedule of processes in a GANTT chart and a table of timing given:
// • an output writer
// • a title for the chart
// • a slice of processes
func SJFPrioritySchedule(w io.Writer, title string, processes []Process) {
	procs := make([]Process, len(processes))
	copy(procs, processes)
	var gantt = []TimeSlice{}
	var t int64 = 0
	var wait = []Process{}
	var finished = []Process{}
	var currentProc Process = Process{ArrivalTime: -1}

	//Execute each time period
	for len(procs) > 0 || len(wait) > 0 || currentProc.ArrivalTime != -1 {
		//If any procs arrive, put them in the queue
		var newArrival = false
		var k = 0
		for i := range procs {
			if procs[i].ArrivalTime == t {
				newArrival = true
				wait = append(wait, procs[i])
			} else {
				procs[k] = procs[i]
				k++
			}
		}
		procs = procs[:k]

		//Empty proc?
		if currentProc.ArrivalTime == -1 { //hack to check if empty
			if len(wait) <= 0 {
				gantt = append(gantt, TimeSlice{
					PID:   -1,
					Start: t,
					Stop:  t + 1,
				})
				continue
			}

			//Find next sjf
			currentProc = PopNextSJFPriority(&wait)
		} else if newArrival { //New arrival this clock?
			if len(wait) <= 0 {
				fmt.Printf("Unreachable code")
				gantt = append(gantt, TimeSlice{
					PID:   -2,
					Start: t,
					Stop:  t + 1,
				})
				continue
			}

			//Check for preempt
			currentProc = PopNextPreemptPriority(currentProc, &wait)
		}

		gantt = append(gantt, TimeSlice{
			PID:   currentProc.ProcessID,
			Start: t,
			Stop:  t + 1,
		})

		//Decr process burst timer
		currentProc.BurstLeft--
		if currentProc.BurstLeft <= 0 {
			currentProc.ExitTime = t + 1
			finished = append(finished, currentProc)
			currentProc = Process{ArrivalTime: -1}
		}

		t++
	}

	//Statistics!!
	//Statistics!!
	schedule := [][]string{}
	var totalWait float64 = 0
	var totalTurnaround float64 = 0
	var lastCompletion int64 = -1
	for _, proc := range finished {
		schedule = append(schedule, []string{
			fmt.Sprint(proc.ProcessID),
			fmt.Sprint(proc.Priority),
			fmt.Sprint(proc.BurstDuration),
			fmt.Sprint(proc.ArrivalTime),
			fmt.Sprint(proc.ExitTime - proc.ArrivalTime - proc.BurstDuration), // waiting time
			fmt.Sprint(proc.ExitTime - proc.ArrivalTime),                      //turnaround
			fmt.Sprint(proc.ExitTime),                                         //completion
		})

		totalWait += float64(proc.ExitTime - proc.ArrivalTime - proc.BurstDuration)
		totalTurnaround += float64(proc.ExitTime - proc.ArrivalTime)
		if proc.ExitTime > lastCompletion {
			lastCompletion = proc.ExitTime
		}
	}

	count := float64(len(processes))
	aveWait := totalWait / count
	aveTurnaround := totalTurnaround / count
	aveThroughput := count / float64(lastCompletion)

	outputTitle(w, title)
	gantt = flattenGantt(gantt)
	outputGantt(w, gantt)
	outputSchedule(w, schedule, aveWait, aveTurnaround, aveThroughput)
}

// SJFSSchedule outputs a schedule of processes in a GANTT chart and a table of timing given:
// • an output writer
// • a title for the chart
// • a slice of processes
func SJFSchedule(w io.Writer, title string, processes []Process) {
	procs := make([]Process, len(processes))
	copy(procs, processes)
	var gantt = []TimeSlice{}
	var t int64 = 0
	var wait = []Process{}
	var finished = []Process{}
	var currentProc Process = Process{ArrivalTime: -1}

	//Execute each time period
	for len(procs) > 0 || len(wait) > 0 || currentProc.ArrivalTime != -1 {
		//If any procs arrive, put them in the queue
		var newArrival = false
		var k = 0
		for i := range procs {
			if procs[i].ArrivalTime == t {
				newArrival = true
				wait = append(wait, procs[i])
			} else {
				procs[k] = procs[i]
				k++
			}
		}
		procs = procs[:k]

		//Empty proc?
		if currentProc.ArrivalTime == -1 { //hack to check if empty
			if len(wait) <= 0 {
				gantt = append(gantt, TimeSlice{
					PID:   -1,
					Start: t,
					Stop:  t + 1,
				})
				continue
			}

			//Find next sjf
			currentProc = PopNextSJF(&wait)
		} else if newArrival { //New arrival this clock?
			if len(wait) <= 0 {
				fmt.Printf("Unreachable code")
				gantt = append(gantt, TimeSlice{
					PID:   -2,
					Start: t,
					Stop:  t + 1,
				})
				continue
			}

			//Check for preempt
			currentProc = PopNextPreempt(currentProc, &wait)
		}

		gantt = append(gantt, TimeSlice{
			PID:   currentProc.ProcessID,
			Start: t,
			Stop:  t + 1,
		})

		//Decr process burst timer
		currentProc.BurstLeft--
		if currentProc.BurstLeft <= 0 {
			currentProc.ExitTime = t + 1
			finished = append(finished, currentProc)
			currentProc = Process{ArrivalTime: -1}
		}

		t++
	}

	//Statistics!!
	//Statistics!!
	schedule := [][]string{}
	var totalWait float64 = 0
	var totalTurnaround float64 = 0
	var lastCompletion int64 = -1
	for _, proc := range finished {
		schedule = append(schedule, []string{
			fmt.Sprint(proc.ProcessID),
			fmt.Sprint(proc.Priority),
			fmt.Sprint(proc.BurstDuration),
			fmt.Sprint(proc.ArrivalTime),
			fmt.Sprint(proc.ExitTime - proc.ArrivalTime - proc.BurstDuration), // waiting time
			fmt.Sprint(proc.ExitTime - proc.ArrivalTime),                      //turnaround
			fmt.Sprint(proc.ExitTime),                                         //completion
		})

		totalWait += float64(proc.ExitTime - proc.ArrivalTime - proc.BurstDuration)
		totalTurnaround += float64(proc.ExitTime - proc.ArrivalTime)
		if proc.ExitTime > lastCompletion {
			lastCompletion = proc.ExitTime
		}
	}

	count := float64(len(processes))
	aveWait := totalWait / count
	aveTurnaround := totalTurnaround / count
	aveThroughput := count / float64(lastCompletion)

	outputTitle(w, title)
	gantt = flattenGantt(gantt)
	outputGantt(w, gantt)
	outputSchedule(w, schedule, aveWait, aveTurnaround, aveThroughput)
}

// RRSchedule outputs a schedule of processes in a GANTT chart and a table of timing given:
// • an output writer
// • a title for the chart
// • a slice of processes
// Note: When a process arrives, it will be added to the END of the queue, and may wait for some time before being executed
// Default time quantum is 2, change the time quantum using the TIME_QUANTUM constant.
func RRSchedule(w io.Writer, title string, processes []Process) {
	procs := make([]Process, len(processes))
	copy(procs, processes)
	finished := []Process{}
	queue := []Process{}
	var gantt = []TimeSlice{}

	var t int64 = 0
	for len(queue) > 0 || len(procs) > 0 {
		//If any procs arrive, put them in the queue
		var k = 0
		for i := range procs {
			if procs[i].ArrivalTime <= t {
				queue = append(queue, procs[i])
			} else {
				procs[k] = procs[i]
				k++
			}
		}
		procs = procs[:k]

		if len(queue) == 0 {
			gantt = append(gantt, TimeSlice{
				Start: t,
				Stop:  t + 1,
				PID:   -1,
			})
			t++
			continue
		}

		currentProcess := queue[0]
		queue = queue[1:]
		if currentProcess.BurstLeft > TIME_QUANTUM {
			// fmt.Printf("Executing process %d for %d units of time\n", currentProcess.ProcessID, timeQuantum)
			currentProcess.BurstLeft -= TIME_QUANTUM
			gantt = append(gantt, TimeSlice{
				Start: t,
				Stop:  TIME_QUANTUM + t,
				PID:   currentProcess.ProcessID,
			})
			t += TIME_QUANTUM

			//in case new processes have been created in the meantime
			var k = 0
			for i := range procs {
				if procs[i].ArrivalTime <= t {
					queue = append(queue, procs[i])
				} else {
					procs[k] = procs[i]
					k++
				}
			}
			procs = procs[:k]

			queue = append(queue, currentProcess)
		} else {
			// fmt.Printf("Executing process %d for %d units of time\n", currentProcess.ProcessID, currentProcess.BurstLeft)
			gantt = append(gantt, TimeSlice{
				Start: t,
				Stop:  currentProcess.BurstLeft + t,
				PID:   currentProcess.ProcessID,
			})
			currentProcess.ExitTime = currentProcess.BurstLeft + t
			t += currentProcess.BurstLeft
			currentProcess.BurstLeft = 0
			finished = append(finished, currentProcess)
		}
	}

	//Statistics!!
	schedule := [][]string{}
	var totalWait float64 = 0
	var totalTurnaround float64 = 0
	var lastCompletion int64 = -1
	for _, proc := range finished {
		schedule = append(schedule, []string{
			fmt.Sprint(proc.ProcessID),
			fmt.Sprint(proc.Priority),
			fmt.Sprint(proc.BurstDuration),
			fmt.Sprint(proc.ArrivalTime),
			fmt.Sprint(proc.ExitTime - proc.ArrivalTime - proc.BurstDuration), // waiting time
			fmt.Sprint(proc.ExitTime - proc.ArrivalTime),                      //turnaround
			fmt.Sprint(proc.ExitTime),                                         //completion
		})

		totalWait += float64(proc.ExitTime - proc.ArrivalTime - proc.BurstDuration)
		totalTurnaround += float64(proc.ExitTime - proc.ArrivalTime)
		if proc.ExitTime > lastCompletion {
			lastCompletion = proc.ExitTime
		}
	}

	count := float64(len(processes))
	aveWait := totalWait / count
	aveTurnaround := totalTurnaround / count
	aveThroughput := count / float64(lastCompletion)

	outputTitle(w, title)
	gantt = flattenGantt(gantt)
	outputGantt(w, gantt)
	outputSchedule(w, schedule, aveWait, aveTurnaround, aveThroughput)
}

//endregion
//region Logical helpers

// Pop the next process in SJF order from the queue, and return it
func PopNextSJF(queue *[]Process) Process {
	var smallest = (*queue)[0]
	var smallesti = 0
	for i, x := range *queue {
		if x.BurstLeft < smallest.BurstLeft {
			smallest = x
			smallesti = i
		}
	}

	*queue = append((*queue)[:smallesti], ((*queue)[smallesti+1:])...)
	return smallest
}

// IF there is a prempting process in the queue, push the currentproc back on the queue, remove the preempting process and return it.
// Otherwise, return the currentProc and do not modify the queue.
func PopNextPreempt(currentProc Process, queue *[]Process) Process {

	var smallest = currentProc
	var smallesti = -1
	for i, x := range *queue {
		if x.BurstLeft < smallest.BurstLeft {
			smallest = x
			smallesti = i
		}
	}

	//No preempt found
	if smallesti == -1 {
		return currentProc
	}

	*queue = append((*queue)[:smallesti], ((*queue)[smallesti+1:])...)
	*queue = append(*queue, currentProc)

	return smallest
}

// Pop the next process in SJFPriority order from the queue, and return it
func PopNextSJFPriority(queue *[]Process) Process {
	var smallest = (*queue)[0]
	var smallesti = 0
	for i, x := range *queue {
		if x.Priority < smallest.Priority {
			smallest = x
			smallesti = i
		} else if x.Priority == smallest.Priority && x.BurstLeft < smallest.BurstLeft {
			smallest = x
			smallesti = i
		}
	}

	*queue = append((*queue)[:smallesti], ((*queue)[smallesti+1:])...)
	return smallest
}

// IF there is a prempting process in the queue, push the currentproc back on the queue, remove the preempting process and return it.
// Otherwise, return the currentProc and do not modify the queue.
func PopNextPreemptPriority(currentProc Process, queue *[]Process) Process {

	var smallest = currentProc
	var smallesti = -1
	for i, x := range *queue {
		if x.Priority < smallest.Priority {
			smallest = x
			smallesti = i
		} else if x.Priority == smallest.Priority && x.BurstLeft < smallest.BurstLeft {
			smallest = x
			smallesti = i
		}
	}

	//No preempt found
	if smallesti == -1 {
		return currentProc
	}

	*queue = append((*queue)[:smallesti], ((*queue)[smallesti+1:])...)
	*queue = append(*queue, currentProc)

	return smallest
}

//endregion
//region Output helpers

// Combines time slices with the same PID.
// Assumes that the gantt chart is continuous.
func flattenGantt(gantt []TimeSlice) []TimeSlice {
	var k = 0
	var buildingTimeSlice = TimeSlice{PID: -1}
	for i, x := range gantt {
		if buildingTimeSlice.PID == -1 {
			buildingTimeSlice.PID = x.PID
			buildingTimeSlice.Start = x.Start
		}

		if buildingTimeSlice.PID != x.PID {
			buildingTimeSlice.Stop = x.Start
			gantt[k] = buildingTimeSlice
			k++
			buildingTimeSlice = TimeSlice{
				PID:   x.PID,
				Start: x.Start,
			}
		}

		if i == len(gantt)-1 {
			buildingTimeSlice.Stop = x.Stop
			gantt[k] = buildingTimeSlice
			k++
		}
	}
	return gantt[:k]
}

func outputTitle(w io.Writer, title string) {
	_, _ = fmt.Fprintln(w, strings.Repeat("-", len(title)*2))
	_, _ = fmt.Fprintln(w, strings.Repeat(" ", len(title)/2), title)
	_, _ = fmt.Fprintln(w, strings.Repeat("-", len(title)*2))
}

func outputGantt(w io.Writer, gantt []TimeSlice) {
	_, _ = fmt.Fprintln(w, "Gantt schedule")
	_, _ = fmt.Fprint(w, "|")
	for i := range gantt {
		pid := fmt.Sprint(gantt[i].PID)
		padding := strings.Repeat(" ", (8-len(pid))/2)
		_, _ = fmt.Fprint(w, padding, pid, padding, "|")
	}
	_, _ = fmt.Fprintln(w)
	for i := range gantt {
		_, _ = fmt.Fprint(w, fmt.Sprint(gantt[i].Start), "\t")
		if len(gantt)-1 == i {
			_, _ = fmt.Fprint(w, fmt.Sprint(gantt[i].Stop))
		}
	}
	_, _ = fmt.Fprintf(w, "\n\n")
}

func outputSchedule(w io.Writer, rows [][]string, wait, turnaround, throughput float64) {
	_, _ = fmt.Fprintln(w, "Schedule table")
	table := tablewriter.NewWriter(w)
	table.SetHeader([]string{"ID", "Priority", "Burst", "Arrival", "Wait", "Turnaround", "Exit"})
	table.AppendBulk(rows)
	table.SetFooter([]string{"", "", "", "",
		fmt.Sprintf("Average\n%.2f", wait),
		fmt.Sprintf("Average\n%.2f", turnaround),
		fmt.Sprintf("Throughput\n%.2f/t", throughput)})
	table.Render()
}

//endregion

//region Loading processes.

var ErrInvalidArgs = errors.New("invalid args")

func loadProcesses(r io.Reader) ([]Process, error) {
	rows, err := csv.NewReader(r).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%w: reading CSV", err)
	}

	processes := make([]Process, len(rows))
	for i := range rows {
		processes[i].ProcessID = mustStrToInt(rows[i][0])
		processes[i].BurstDuration = mustStrToInt(rows[i][1])
		processes[i].ArrivalTime = mustStrToInt(rows[i][2])
		processes[i].BurstLeft = processes[i].BurstDuration
		if len(rows[i]) == 4 {
			processes[i].Priority = mustStrToInt(rows[i][3])
		}
	}

	return processes, nil
}

func mustStrToInt(s string) int64 {
	i, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	return i
}

//endregion
