package monitor

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Stats struct {
	CPU     CPUStats
	Memory  MemStats
	Disk    DiskStats
	Network NetStats
	Uptime  string
	Docker  DockerStats
}

type CPUStats struct {
	UsagePercent float64
	Cores        int
}

type MemStats struct {
	TotalMB     int64
	UsedMB      int64
	AvailableMB int64
	Percent     float64
}

type DiskStats struct {
	TotalGB   float64
	UsedGB    float64
	FreeGB    float64
	Percent   float64
	MountPath string
}

type NetStats struct {
	RxBytes int64
	TxBytes int64
	RxRate  string
	TxRate  string
}

type DockerStats struct {
	RunningContainers int
	TotalImages       int
}

type Monitor struct {
	mu       sync.Mutex
	prevIdle int64
	prevTotal int64
	prevRx   int64
	prevTx   int64
	prevTime time.Time
	lastNet  NetStats
}

func New() *Monitor {
	m := &Monitor{prevTime: time.Now()}
	m.sampleCPU()
	m.sampleNet()
	return m
}

func (m *Monitor) GetStats() Stats {
	return Stats{
		CPU:     m.getCPU(),
		Memory:  getMemory(),
		Disk:    getDisk(),
		Network: m.getNetwork(),
		Uptime:  getUptime(),
		Docker:  getDocker(),
	}
}

func (m *Monitor) getCPU() CPUStats {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return CPUStats{}
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 {
		return CPUStats{}
	}

	fields := strings.Fields(lines[0])
	if len(fields) < 5 || fields[0] != "cpu" {
		return CPUStats{}
	}

	var total, idle int64
	for i := 1; i < len(fields); i++ {
		val, _ := strconv.ParseInt(fields[i], 10, 64)
		total += val
		if i == 4 {
			idle = val
		}
	}

	cores := 0
	for _, line := range lines[1:] {
		if strings.HasPrefix(line, "cpu") && len(line) > 3 && line[3] >= '0' && line[3] <= '9' {
			cores++
		}
	}

	deltaTotal := total - m.prevTotal
	deltaIdle := idle - m.prevIdle
	m.prevTotal = total
	m.prevIdle = idle

	usage := 0.0
	if deltaTotal > 0 {
		usage = float64(deltaTotal-deltaIdle) / float64(deltaTotal) * 100
	}

	return CPUStats{UsagePercent: usage, Cores: cores}
}

func (m *Monitor) sampleCPU() {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return
	}
	fields := strings.Fields(strings.Split(string(data), "\n")[0])
	if len(fields) < 5 {
		return
	}
	var total int64
	for i := 1; i < len(fields); i++ {
		val, _ := strconv.ParseInt(fields[i], 10, 64)
		total += val
	}
	idle, _ := strconv.ParseInt(fields[4], 10, 64)
	m.mu.Lock()
	m.prevTotal = total
	m.prevIdle = idle
	m.mu.Unlock()
}

func getMemory() MemStats {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return MemStats{}
	}

	values := map[string]int64{}
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			key := strings.TrimSuffix(parts[0], ":")
			val, _ := strconv.ParseInt(parts[1], 10, 64)
			values[key] = val
		}
	}

	total := values["MemTotal"]
	available := values["MemAvailable"]
	used := total - available

	pct := 0.0
	if total > 0 {
		pct = float64(used) / float64(total) * 100
	}

	return MemStats{
		TotalMB:     total / 1024,
		UsedMB:      used / 1024,
		AvailableMB: available / 1024,
		Percent:     pct,
	}
}

func getDisk() DiskStats {
	out, err := exec.Command("df", "-B1", "/").Output()
	if err != nil {
		return DiskStats{}
	}
	lines := strings.Split(string(out), "\n")
	if len(lines) < 2 {
		return DiskStats{}
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 6 {
		return DiskStats{}
	}

	total, _ := strconv.ParseFloat(fields[1], 64)
	used, _ := strconv.ParseFloat(fields[2], 64)
	free, _ := strconv.ParseFloat(fields[3], 64)

	gb := 1024.0 * 1024.0 * 1024.0
	pct := 0.0
	if total > 0 {
		pct = used / total * 100
	}

	return DiskStats{
		TotalGB:   total / gb,
		UsedGB:    used / gb,
		FreeGB:    free / gb,
		Percent:   pct,
		MountPath: fields[5],
	}
}

func (m *Monitor) getNetwork() NetStats {
	m.mu.Lock()
	defer m.mu.Unlock()

	rx, tx := readNetBytes()
	now := time.Now()
	elapsed := now.Sub(m.prevTime).Seconds()

	var rxRate, txRate string
	if elapsed > 0 && m.prevRx > 0 {
		rxRate = formatRate(float64(rx-m.prevRx) / elapsed)
		txRate = formatRate(float64(tx-m.prevTx) / elapsed)
	}

	m.prevRx = rx
	m.prevTx = tx
	m.prevTime = now

	return NetStats{
		RxBytes: rx,
		TxBytes: tx,
		RxRate:  rxRate,
		TxRate:  txRate,
	}
}

func (m *Monitor) sampleNet() {
	rx, tx := readNetBytes()
	m.mu.Lock()
	m.prevRx = rx
	m.prevTx = tx
	m.prevTime = time.Now()
	m.mu.Unlock()
}

func readNetBytes() (rx, tx int64) {
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n")[2:] {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		iface := strings.TrimSuffix(fields[0], ":")
		if iface == "lo" {
			continue
		}
		r, _ := strconv.ParseInt(fields[1], 10, 64)
		t, _ := strconv.ParseInt(fields[9], 10, 64)
		rx += r
		tx += t
	}
	return
}

func getUptime() string {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return "unknown"
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return "unknown"
	}
	secs, _ := strconv.ParseFloat(fields[0], 64)
	d := time.Duration(secs) * time.Second
	days := int(d.Hours() / 24)
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

func getDocker() DockerStats {
	out, _ := exec.Command("docker", "ps", "-q").Output()
	running := len(strings.Fields(string(out)))
	out, _ = exec.Command("docker", "images", "-q").Output()
	images := len(strings.Fields(string(out)))
	return DockerStats{RunningContainers: running, TotalImages: images}
}

func formatRate(bytesPerSec float64) string {
	switch {
	case bytesPerSec >= 1024*1024*1024:
		return fmt.Sprintf("%.1f GB/s", bytesPerSec/(1024*1024*1024))
	case bytesPerSec >= 1024*1024:
		return fmt.Sprintf("%.1f MB/s", bytesPerSec/(1024*1024))
	case bytesPerSec >= 1024:
		return fmt.Sprintf("%.1f KB/s", bytesPerSec/1024)
	default:
		return fmt.Sprintf("%.0f B/s", bytesPerSec)
	}
}

func FormatBytes(b int64) string {
	switch {
	case b >= 1024*1024*1024:
		return fmt.Sprintf("%.1f GB", float64(b)/(1024*1024*1024))
	case b >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
	case b >= 1024:
		return fmt.Sprintf("%.1f KB", float64(b)/1024)
	default:
		return fmt.Sprintf("%d B", b)
	}
}
