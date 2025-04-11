package main

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/procfs"
	"github.com/prometheus/procfs/blockdevice"
	"golang.org/x/sys/unix"
)

type Node struct {
	proc        procfs.FS
	blockdevice blockdevice.FS
	cpuStat     procfs.CPUStat
	netStats    procfs.NetDev
	diskioStats map[string]blockdevice.IOStats

	cpu    *prometheus.CounterVec
	mem    *prometheus.GaugeVec
	swap   *prometheus.GaugeVec
	net    *prometheus.CounterVec
	disk   *prometheus.GaugeVec
	diskio *prometheus.CounterVec
}

func NewNode() (*Node, error) {
	e := &Node{
		cpu: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "node_cpu_seconds_total",
			Help: "Total CPU time in seconds.",
		}, []string{"mode"}),
		mem: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "node_mem_bytes",
			Help: "Memory size in bytes.",
		}, []string{"type"}),
		swap: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "node_swap_bytes",
			Help: "Swap size in bytes.",
		}, []string{"type"}),
		net: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "node_net_bytes_total",
			Help: "Network traffic in bytes.",
		}, []string{"interface", "type"}),
		disk: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "node_disk_kilobytes",
			Help: "Hard disk size in kilobytes.",
		}, []string{"device", "mount", "type"}),
		diskio: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "node_diskio_seconds_total",
			Help: "Hard disk time in seconds.",
		}, []string{"device", "type"}),
	}
	e.Init()
	return e, nil
}

func (e *Node) Init() {
	proc, err := procfs.NewFS("/proc")
	if err != nil {
		return nil, err
	}
	blockdev, err := blockdevice.NewFS("/proc", "/sys")
	if err != nil {
		return nil, err
	}

	e.proc = proc
	e.blockdevice = blockdev
	e.diskioStats = map[string]blockdevice.IOStats{}

	e.updateCPUStat()
	e.updateNetStats()
	e.updateDiskIOStats()
}

func (e *Node) Close() error {
	return nil
}

func (e *Node) Describe(ch chan<- *prometheus.Desc) {
	e.cpu.Describe(ch)
	e.mem.Describe(ch)
	e.swap.Describe(ch)
	e.net.Describe(ch)
	e.disk.Describe(ch)
	e.diskio.Describe(ch)
}

func (e *Node) Collect(ch chan<- prometheus.Metric) {
	t := time.Now()
	metrics := e.Read()
	e.cpu.WithLabelValues("system").Add(metrics.CPU.System)
	e.cpu.WithLabelValues("user").Add(metrics.CPU.User)
	e.cpu.WithLabelValues("iowait").Add(metrics.CPU.IOWait)
	e.cpu.WithLabelValues("idle").Add(metrics.CPU.Idle)
	e.cpu.WithLabelValues("rest").Add(metrics.CPU.Rest)
	e.cpu.Collect(ch)

	e.mem.WithLabelValues("total").Set(metrics.Mem.Total)
	e.mem.WithLabelValues("used").Set(metrics.Mem.Used)
	e.mem.WithLabelValues("free").Set(metrics.Mem.Free)
	e.mem.WithLabelValues("shared").Set(metrics.Mem.Shared)
	e.mem.WithLabelValues("buffers").Set(metrics.Mem.Buffers)
	e.mem.WithLabelValues("cache").Set(metrics.Mem.Cache)
	e.mem.WithLabelValues("available").Set(metrics.Mem.Available)
	e.mem.Collect(ch)

	e.swap.WithLabelValues("total").Set(metrics.Swap.Total)
	e.swap.WithLabelValues("used").Set(metrics.Swap.Used)
	e.swap.Collect(ch)

	for _, item := range metrics.Net {
		e.net.WithLabelValues(item.Interface, "rx").Add(item.Received)
		e.net.WithLabelValues(item.Interface, "tx").Add(item.Transmitted)
	}
	e.net.Collect(ch)

	for _, item := range metrics.Disk {
		e.disk.WithLabelValues(item.Device, item.Mount, "total").Set(item.Total)
		e.disk.WithLabelValues(item.Device, item.Mount, "used").Set(item.Used)
		e.disk.WithLabelValues(item.Device, item.Mount, "free").Set(item.Free)
		e.disk.WithLabelValues(item.Device, item.Mount, "available").Set(item.Available)
	}
	e.disk.Collect(ch)

	for _, item := range metrics.IO {
		e.diskio.WithLabelValues(item.Device, "total").Add(item.Total)
		e.diskio.WithLabelValues(item.Device, "read").Add(item.Read)
		e.diskio.WithLabelValues(item.Device, "write").Add(item.Write)
	}
	e.diskio.Collect(ch)
	Debug.Println("collect duration for node:", time.Since(t))
}

type NodeNetMetrics struct {
	Interface   string
	Received    float64
	Transmitted float64
}

type NodeDiskMetrics struct {
	Device    string
	Mount     string
	Total     float64
	Used      float64
	Free      float64
	Available float64
}

type NodeIOMetrics struct {
	Device string
	Total  float64
	Read   float64
	Write  float64
}

type NodeMetrics struct {
	CPU struct {
		System float64
		User   float64
		IOWait float64
		Idle   float64
		Rest   float64
	}
	Mem struct {
		Total     float64
		Used      float64
		Free      float64
		Shared    float64
		Buffers   float64
		Cache     float64
		Available float64
	}
	Swap struct {
		Total float64
		Used  float64
	}
	Net  []NodeNetMetrics
	Disk []NodeDiskMetrics
	IO   []NodeIOMetrics
}

func (e *Node) Read() NodeMetrics {
	metrics := NodeMetrics{}

	cpuStat, err := e.updateCPUStat()
	if err != nil {
		Error.Println(err)
	}
	metrics.CPU.System = math.Max(0.0, cpuStat.System)
	metrics.CPU.User = math.Max(0.0, cpuStat.User+cpuStat.Nice)
	metrics.CPU.IOWait = math.Max(0.0, cpuStat.Iowait)
	metrics.CPU.Idle = math.Max(0.0, cpuStat.Idle)
	metrics.CPU.Rest = math.Max(0.0, cpuStat.IRQ+cpuStat.SoftIRQ+cpuStat.Steal+cpuStat.Guest+cpuStat.GuestNice)

	memStat, err := e.proc.Meminfo()
	if err != nil {
		Error.Println(err)
	}
	metrics.Mem.Total = float64(*memStat.MemTotal)
	metrics.Mem.Used = float64(*memStat.MemTotal - *memStat.MemAvailable)
	metrics.Mem.Free = float64(*memStat.MemFree)
	metrics.Mem.Shared = float64(*memStat.Shmem)
	metrics.Mem.Buffers = float64(*memStat.Buffers)
	metrics.Mem.Cache = float64(*memStat.Cached + *memStat.SReclaimable)
	metrics.Mem.Available = float64(*memStat.MemAvailable)
	metrics.Swap.Total = float64(*memStat.SwapTotal)
	metrics.Swap.Used = float64(*memStat.SwapTotal - *memStat.SwapFree)

	netStats, err := e.updateNetStats()
	if err != nil {
		Error.Println(err)
	}
	for netif, stat := range netStats {
		if netif != "lo" {
			metrics.Net = append(metrics.Net, NodeNetMetrics{
				Interface:   netif,
				Received:    math.Max(0.0, float64(stat.RxBytes)),
				Transmitted: math.Max(0.0, float64(stat.TxBytes)),
			})
		}
	}

	diskStats, err := readDiskStats()
	if err != nil {
		Error.Println(err)
	}
	for disk, stat := range diskStats {
		metrics.Disk = append(metrics.Disk, NodeDiskMetrics{
			Device:    disk.device,
			Mount:     disk.mount,
			Total:     float64(stat.Total),
			Used:      float64(stat.Total - stat.Available),
			Free:      float64(stat.Free),
			Available: float64(stat.Available),
		})
	}

	ioStats, err := e.updateDiskIOStats()
	if err != nil {
		Error.Println(err)
	}
	for _, stat := range ioStats {
		metrics.IO = append(metrics.IO, NodeIOMetrics{
			Device: stat.Info.DeviceName,
			Total:  float64(stat.IOStats.IOsTotalTicks) / 1000.0,
			Read:   float64(stat.IOStats.ReadTicks) / 1000.0,
			Write:  float64(stat.IOStats.WriteTicks) / 1000.0,
		})
	}
	return metrics
}

func (e *Node) updateCPUStat() (procfs.CPUStat, error) {
	stat, err := e.proc.Stat()
	if err != nil {
		return procfs.CPUStat{}, err
	}

	cur := procfs.CPUStat{}
	for _, cpu := range stat.CPU {
		cur.User += cpu.User
		cur.Nice += cpu.Nice
		cur.System += cpu.System
		cur.Idle += cpu.Idle
		cur.Iowait += cpu.Iowait
		cur.IRQ += cpu.IRQ
		cur.SoftIRQ += cpu.SoftIRQ
		cur.Steal += cpu.Steal
		cur.Guest += cpu.Guest
		cur.GuestNice += cpu.GuestNice
	}

	// this is fine when cur uint64 wraps around to zero
	diff := cur
	diff.User -= e.cpuStat.User
	diff.Nice -= e.cpuStat.Nice
	diff.System -= e.cpuStat.System
	diff.Idle -= e.cpuStat.Idle
	diff.Iowait -= e.cpuStat.Iowait
	diff.IRQ -= e.cpuStat.IRQ
	diff.SoftIRQ -= e.cpuStat.SoftIRQ
	diff.Steal -= e.cpuStat.Steal
	diff.Guest -= e.cpuStat.Guest
	diff.GuestNice -= e.cpuStat.GuestNice
	e.cpuStat = cur
	return diff, nil
}

func (e *Node) updateNetStats() (procfs.NetDev, error) {
	cur, err := e.proc.NetDev()
	if err != nil {
		return nil, err
	}

	diff := procfs.NetDev{}
	for netif, stat := range e.netStats {
		// this is fine when cur uint64 wraps around to zero
		diff[netif] = procfs.NetDevLine{
			RxBytes:      cur[netif].RxBytes - stat.RxBytes,
			RxPackets:    cur[netif].RxPackets - stat.RxPackets,
			RxErrors:     cur[netif].RxErrors - stat.RxErrors,
			RxDropped:    cur[netif].RxDropped - stat.RxDropped,
			RxFIFO:       cur[netif].RxFIFO - stat.RxFIFO,
			RxFrame:      cur[netif].RxFrame - stat.RxFrame,
			RxCompressed: cur[netif].RxCompressed - stat.RxCompressed,
			RxMulticast:  cur[netif].RxMulticast - stat.RxMulticast,
			TxBytes:      cur[netif].TxBytes - stat.TxBytes,
			TxPackets:    cur[netif].TxPackets - stat.TxPackets,
			TxErrors:     cur[netif].TxErrors - stat.TxErrors,
			TxDropped:    cur[netif].TxDropped - stat.TxDropped,
			TxFIFO:       cur[netif].TxFIFO - stat.TxFIFO,
			TxCollisions: cur[netif].TxCollisions - stat.TxCollisions,
			TxCarrier:    cur[netif].TxCarrier - stat.TxCarrier,
			TxCompressed: cur[netif].TxCompressed - stat.TxCompressed,
		}
	}
	e.netStats = cur
	return diff, err
}

func (e *Node) updateDiskIOStats() ([]blockdevice.Diskstats, error) {
	stats, err := e.blockdevice.ProcDiskstats()
	if err != nil {
		return nil, err
	}

	diff := []blockdevice.Diskstats{}
	for _, cur := range stats {
		// this is fine when cur uint64 wraps around to zero
		stat := e.diskioStats[cur.Info.DeviceName]
		diff = append(diff, blockdevice.Diskstats{
			Info: cur.Info,
			IOStats: blockdevice.IOStats{
				ReadIOs:                cur.IOStats.ReadIOs - stat.ReadIOs,
				ReadMerges:             cur.IOStats.ReadMerges - stat.ReadMerges,
				ReadSectors:            cur.IOStats.ReadSectors - stat.ReadSectors,
				ReadTicks:              cur.IOStats.ReadTicks - stat.ReadTicks,
				WriteIOs:               cur.IOStats.WriteIOs - stat.WriteIOs,
				WriteMerges:            cur.IOStats.WriteMerges - stat.WriteMerges,
				WriteSectors:           cur.IOStats.WriteSectors - stat.WriteSectors,
				WriteTicks:             cur.IOStats.WriteTicks - stat.WriteTicks,
				IOsInProgress:          cur.IOStats.IOsInProgress - stat.IOsInProgress,
				IOsTotalTicks:          cur.IOStats.IOsTotalTicks - stat.IOsTotalTicks,
				WeightedIOTicks:        cur.IOStats.WeightedIOTicks - stat.WeightedIOTicks,
				DiscardIOs:             cur.IOStats.DiscardIOs - stat.DiscardIOs,
				DiscardMerges:          cur.IOStats.DiscardMerges - stat.DiscardMerges,
				DiscardSectors:         cur.IOStats.DiscardSectors - stat.DiscardSectors,
				DiscardTicks:           cur.IOStats.DiscardTicks - stat.DiscardTicks,
				FlushRequestsCompleted: cur.IOStats.FlushRequestsCompleted - stat.FlushRequestsCompleted,
				TimeSpentFlushing:      cur.IOStats.TimeSpentFlushing - stat.TimeSpentFlushing,
			},
			IoStatsCount: cur.IoStatsCount,
		})
		e.diskioStats[cur.Info.DeviceName] = cur.IOStats
	}
	return diff, nil
}

type disk struct {
	device string
	mount  string
}

type diskStat struct {
	Total     uint64
	Free      uint64
	Available uint64
}

func readDiskStats() (map[disk]diskStat, error) {
	mounts, err := os.Open("/proc/mounts")
	if err != nil {
		return nil, err
	}

	n := 0
	devices := []string{}
	mountpoints := []string{}
	scanner := bufio.NewScanner(mounts)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 {
			mounts.Close()
			return nil, fmt.Errorf("/proc/mounts:%v: bad mount point", n)
		} else if !strings.HasPrefix(fields[0], "/dev/") {
			continue
		}

		fields[1] = strings.Replace(fields[1], "\\040", " ", -1)
		fields[1] = strings.Replace(fields[1], "\\011", "\t", -1)
		devices = append(devices, fields[0])
		mountpoints = append(mountpoints, fields[1])
		n++
	}
	if err := mounts.Close(); err != nil {
		return nil, err
	}

	stats := map[disk]diskStat{}
	for i, device := range devices {
		buf := unix.Statfs_t{}
		if err := unix.Statfs(mountpoints[i], &buf); err != nil {
			return nil, err
		}
		stats[disk{device[5:], mountpoints[i]}] = diskStat{
			Total:     uint64(buf.Bsize) * buf.Blocks / 1000,
			Free:      uint64(buf.Bsize) * buf.Bfree / 1000,
			Available: uint64(buf.Bsize) * buf.Bavail / 1000,
		}
	}
	return stats, nil
}
