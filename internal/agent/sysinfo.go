package agent

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// SystemStats is a host metrics sample included in heartbeats.
type SystemStats struct {
	CPUPct    float64 `json:"cpuPct"`
	MemUsed   int64   `json:"memUsed"`
	MemTotal  int64   `json:"memTotal"`
	DiskUsed  int64   `json:"diskUsed"`
	DiskTotal int64   `json:"diskTotal"`
	Load1     float64 `json:"load1"`
	UptimeSec int64   `json:"uptimeSec"`
}

type sampler struct {
	lastTotal uint64
	lastIdle  uint64
}

func (s *sampler) Sample(dataDir string) SystemStats {
	out := SystemStats{}
	if total, idle, ok := readCPU(); ok {
		if s.lastTotal > 0 && total > s.lastTotal {
			dt := float64(total - s.lastTotal)
			di := float64(idle - s.lastIdle)
			out.CPUPct = (dt - di) / dt * 100
			if out.CPUPct < 0 {
				out.CPUPct = 0
			}
		}
		s.lastTotal, s.lastIdle = total, idle
	}
	out.MemTotal, out.MemUsed = readMem()
	out.Load1 = readLoad()
	out.UptimeSec = readUptime()
	out.DiskTotal, out.DiskUsed = readDisk(dataDir)
	return out
}

func readCPU() (total, idle uint64, ok bool) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return 0, 0, false
	}
	fields := strings.Fields(sc.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, false
	}
	for i, v := range fields[1:] {
		n, _ := strconv.ParseUint(v, 10, 64)
		total += n
		if i == 3 || i == 4 { // idle + iowait
			idle += n
		}
	}
	return total, idle, true
}

func readMem() (total, used int64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	vals := map[string]int64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		n, _ := strconv.ParseInt(fields[1], 10, 64)
		vals[strings.TrimSuffix(fields[0], ":")] = n * 1024
	}
	total = vals["MemTotal"]
	avail := vals["MemAvailable"]
	if avail == 0 {
		avail = vals["MemFree"] + vals["Buffers"] + vals["Cached"]
	}
	used = total - avail
	if used < 0 {
		used = 0
	}
	return total, used
}

func readLoad() float64 {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(fields[0], 64)
	return v
}

func readUptime() int64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(fields[0], 64)
	return int64(v)
}

func readDisk(dir string) (total, used int64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, 0
	}
	total = int64(st.Blocks) * int64(st.Bsize)
	free := int64(st.Bavail) * int64(st.Bsize)
	used = total - free
	if used < 0 {
		used = 0
	}
	return total, used
}
