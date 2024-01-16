package main

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/coreos/go-systemd/v22/dbus"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/procfs"
	"github.com/prometheus/procfs/blockdevice"
	"golang.org/x/sys/unix"
)

type Node struct {
	services []string

	proc        procfs.FS
	blockdevice blockdevice.FS
	cpuStat     procfs.CPUStat
	netStats    procfs.NetDev
	diskioStats map[string]blockdevice.IOStats

	cpu     *prometheus.CounterVec
	mem     *prometheus.GaugeVec
	swap    *prometheus.GaugeVec
	net     *prometheus.CounterVec
	disk    *prometheus.GaugeVec
	diskio  *prometheus.CounterVec
	service *prometheus.GaugeVec
}

func NewNode() (*Node, error) {
	proc, err := procfs.NewFS("/proc")
	if err != nil {
		return nil, err
	}
	blockdev, err := blockdevice.NewFS("/proc", "/sys")
	if err != nil {
		return nil, err
	}

	node := &Node{
		proc:        proc,
		blockdevice: blockdev,
		diskioStats: map[string]blockdevice.IOStats{},

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
		}, []string{"device", "type"}),
		diskio: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "node_diskio_seconds_total",
			Help: "Hard disk time in seconds.",
		}, []string{"device", "type"}),
		service: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "node_service_active",
			Help: "Systemd service active.",
		}, []string{"service"}),
	}
	if _, err = node.updateCPUStat(); err != nil {
		return nil, err
	} else if _, err = node.updateNetStats(); err != nil {
		return nil, err
	} else if _, err = node.updateDiskIOStats(); err != nil {
		return nil, err
	}
	return node, nil
}

func (e *Node) AddServices(services ...string) {
	for _, service := range services {
		for i := range e.services {
			if service == e.services[i] {
				return
			}
		}
		e.services = append(e.services, service)
	}
}

func (e *Node) Describe(ch chan<- *prometheus.Desc) {
	e.cpu.Describe(ch)
	e.mem.Describe(ch)
	e.swap.Describe(ch)
	e.net.Describe(ch)
	e.disk.Describe(ch)
	e.diskio.Describe(ch)
	e.service.Describe(ch)
}

func (e *Node) Collect(ch chan<- prometheus.Metric) {
	t0 := time.Now()
	defer func() {
		Info.Println("collect duration total:", time.Since(t0))
	}()
	t := time.Now()
	cpuStat, err := e.updateCPUStat()
	if err != nil {
		Error.Println(err)
	} else {
		e.cpu.WithLabelValues("system").Add(math.Max(0.0, cpuStat.System))
		e.cpu.WithLabelValues("user").Add(math.Max(0.0, cpuStat.User+cpuStat.Nice))
		e.cpu.WithLabelValues("iowait").Add(math.Max(0.0, cpuStat.Iowait))
		e.cpu.WithLabelValues("idle").Add(math.Max(0.0, cpuStat.Idle))
		e.cpu.WithLabelValues("rest").Add(math.Max(0.0, cpuStat.IRQ+cpuStat.SoftIRQ+cpuStat.Steal+cpuStat.Guest+cpuStat.GuestNice))
		e.cpu.Collect(ch)
	}
	Info.Println("collect duration for node_cpu:", time.Since(t))

	t = time.Now()
	memStat, err := e.proc.Meminfo()
	if err != nil {
		Error.Println(err)
	} else {
		e.mem.WithLabelValues("total").Set(float64(*memStat.MemTotal))
		e.mem.WithLabelValues("used").Set(float64(*memStat.MemTotal - *memStat.MemAvailable))
		e.mem.WithLabelValues("free").Set(float64(*memStat.MemFree))
		e.mem.WithLabelValues("shared").Set(float64(*memStat.Shmem))
		e.mem.WithLabelValues("buffers").Set(float64(*memStat.Buffers))
		e.mem.WithLabelValues("cache").Set(float64(*memStat.Cached + *memStat.SReclaimable))
		e.mem.WithLabelValues("available").Set(float64(*memStat.MemAvailable))
		e.mem.Collect(ch)

		e.swap.WithLabelValues("total").Set(float64(*memStat.SwapTotal))
		e.swap.WithLabelValues("used").Set(float64(*memStat.SwapTotal - *memStat.SwapFree))
		e.swap.Collect(ch)
	}
	Info.Println("collect duration for node_mem/node_swap:", time.Since(t))

	t = time.Now()
	netStats, err := e.updateNetStats()
	if err != nil {
		Error.Println(err)
	} else {
		for netif, stat := range netStats {
			if netif != "lo" {
				e.net.WithLabelValues(netif, "rx").Add(math.Max(0.0, float64(stat.RxBytes)))
				e.net.WithLabelValues(netif, "tx").Add(math.Max(0.0, float64(stat.TxBytes)))
			}
		}
		e.net.Collect(ch)
	}
	Info.Println("collect duration for node_net:", time.Since(t))

	t = time.Now()
	diskStats, err := readDiskStats()
	if err != nil {
		Error.Println(err)
	} else {
		for device, stat := range diskStats {
			e.disk.WithLabelValues(device, "total").Set(float64(stat.Total))
			e.disk.WithLabelValues(device, "used").Set(float64(stat.Total - stat.Available))
			e.disk.WithLabelValues(device, "free").Set(float64(stat.Free))
			e.disk.WithLabelValues(device, "available").Set(float64(stat.Available))
		}
		e.disk.Collect(ch)
	}
	Info.Println("collect duration for node_disk:", time.Since(t))

	t = time.Now()
	ioStats, err := e.updateDiskIOStats()
	if err != nil {
		Error.Println(err)
	} else {
		for _, stat := range ioStats {
			device := stat.Info.DeviceName
			e.diskio.WithLabelValues(device, "total").Add(float64(stat.IOStats.IOsTotalTicks) / 1000.0)
			e.diskio.WithLabelValues(device, "read").Add(float64(stat.IOStats.ReadTicks) / 1000.0)
			e.diskio.WithLabelValues(device, "write").Add(float64(stat.IOStats.WriteTicks) / 1000.0)
		}
		e.diskio.Collect(ch)
	}
	Info.Println("collect duration for node_diskio:", time.Since(t))

	t = time.Now()
	serviceStats, err := readSystemd(context.TODO(), e.services)
	if err != nil {
		Error.Println(err)
	} else {
		for _, stat := range serviceStats {
			active := 0.0
			if stat.ActiveState == "active" || stat.ActiveState == "reloading" {
				active = 1.0
			}
			e.service.WithLabelValues(stat.Id).Set(active)
		}
		e.service.Collect(ch)
	}
	Info.Println("collect duration for node_service:", time.Since(t))
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

type diskStat struct {
	Total     uint64
	Free      uint64
	Available uint64
}

func readDiskStats() (map[string]diskStat, error) {
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

	stats := map[string]diskStat{}
	for i, device := range devices {
		buf := unix.Statfs_t{}
		if err := unix.Statfs(mountpoints[i], &buf); err != nil {
			return nil, err
		}
		stats[device[5:]] = diskStat{
			Total:     uint64(buf.Bsize) * buf.Blocks / 1024,
			Free:      uint64(buf.Bsize) * buf.Bfree / 1024,
			Available: uint64(buf.Bsize) * buf.Bavail / 1024,
		}
	}
	return stats, nil
}

type systemdUnit struct {
	Id          string
	ActiveState string
}

func readSystemd(ctx context.Context, services []string) ([]systemdUnit, error) {
	conn, err := dbus.NewWithContext(ctx)
	if err != nil {
		return nil, err
	}

	units := []systemdUnit{}
	for _, service := range services {
		props, err := conn.GetUnitPropertiesContext(ctx, service+".service")
		if err != nil {
			return nil, err
		}
		unit := systemdUnit{}
		if s, ok := props["Id"].(string); ok {
			if strings.HasSuffix(s, ".service") {
				s = s[:len(s)-8]
			}
			unit.Id = s
		}
		if s, ok := props["ActiveState"].(string); ok {
			unit.ActiveState = s
		}
		units = append(units, unit)
	}
	return units, nil
}
