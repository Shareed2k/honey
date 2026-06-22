package cli

import (
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"

	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/sshclient"
)

// Profiler collects system metrics like CPU, memory, and network usage.
type Profiler struct {
	stopCh   chan struct{}
	proc     *process.Process
	cpuSum   float64
	cpuCount int64
	memPeak  uint64

	startNet net.IOCountersStat
}

// StartProfiler initializes and starts collecting system metrics in the background.
func StartProfiler() *Profiler {
	pid := int32(os.Getpid()) // #nosec G115 -- PIDs always fit in int32
	proc, err := process.NewProcess(pid)
	if err != nil {
		fmt.Fprintf(os.Stderr, "profiler warning: could not track process: %v\n", err)
	}

	startNet := getNetworkStats()

	p := &Profiler{
		stopCh:   make(chan struct{}),
		proc:     proc,
		startNet: startNet,
	}

	go p.collectLoop()
	return p
}

func getNetworkStats() net.IOCountersStat {
	stats, err := net.IOCounters(false)
	if err == nil && len(stats) > 0 {
		return stats[0]
	}
	return net.IOCountersStat{}
}

func (p *Profiler) collectLoop() {
	if p.proc == nil {
		return
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	// Initial reading to clear the baseline
	_, _ = p.proc.Percent(0)

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			pct, err := p.proc.Percent(0)
			if err == nil {
				p.cpuSum += pct
				p.cpuCount++
			}
			mem, err := p.proc.MemoryInfo()
			if err == nil {
				if mem.RSS > p.memPeak {
					p.memPeak = mem.RSS
				}
			}
		}
	}
}

// StopAndPrintReport stops the profiler and prints the collected metrics and SSH cache stats.
func (p *Profiler) StopAndPrintReport(targets, steps int, cache *engine.ClientCache) {
	close(p.stopCh)
	endNet := getNetworkStats()

	var cpuAvg float64
	if p.cpuCount > 0 {
		cpuAvg = p.cpuSum / float64(p.cpuCount)
	}

	memMB := float64(p.memPeak) / 1024 / 1024
	sentMB := float64(endNet.BytesSent-p.startNet.BytesSent) / 1024 / 1024
	recvMB := float64(endNet.BytesRecv-p.startNet.BytesRecv) / 1024 / 1024

	stats := engine.CacheStats{}
	if cache != nil {
		stats = cache.Stats()
	}
	sftpReused := atomic.LoadInt64(&sshclient.SFTPSessionsReused)

	fmt.Println("\nExecution profile")
	fmt.Println("────────────────────────")
	fmt.Printf("Targets:              %d\n", targets)
	fmt.Printf("Steps:                %d\n", steps)
	fmt.Printf("SSH connects:         %d\n", stats.DialAttempts)
	fmt.Printf("SSH sessions reused:  %d\n", stats.Hits)
	fmt.Printf("SFTP clients reused:  %d\n", sftpReused)
	fmt.Println("Local machine:")
	fmt.Printf("CPU avg:              %.0f%%\n", cpuAvg)
	fmt.Printf("Memory peak:          %.0f MB\n", memMB)
	fmt.Printf("Network sent:         %.0f MB\n", sentMB)
	fmt.Printf("Network received:     %.0f MB\n", recvMB)
}
