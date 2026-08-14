// Command setclock sets the onboard clock of a Redragon K724-RGB-PRO
// keyboard from the local time.
package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	hid "github.com/sstallion/go-hid"

	"k724tool/internal/protocol"
)

const readTimeout = 1 * time.Second

var testTime = time.Date(2000, time.January, 1, 23, 59, 59, 0, time.Local)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "setclock:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("setclock", flag.ContinueOnError)
	var (
		vidFlag  string
		pidFlag  string
		path     string
		wireless bool
		list     bool
		dryRun   bool
		test     bool
	)
	fs.StringVar(&vidFlag, "vid", "", "USB vendor ID override, for example 0x320f")
	fs.StringVar(&pidFlag, "pid", "", "USB product ID override, for example 0x511b")
	fs.StringVar(&path, "path", "", "exact hidapi device path")
	fs.BoolVar(&wireless, "wireless", false, "target the 2.4 GHz wireless receiver instead of the wired keyboard")
	fs.BoolVar(&list, "list", false, "list candidate HID interfaces and exit")
	fs.BoolVar(&dryRun, "dry-run", false, "print the reports without opening a device")
	fs.BoolVar(&test, "test", false, "set an obviously fake time (2000-01-01 23:59:59) to confirm the write took effect")
	if err := fs.Parse(args); err != nil {
		return err
	}

	when := time.Now()
	if test {
		when = testTime
	}

	if list {
		return listCandidates()
	}

	if dryRun {
		for _, step := range protocol.ClockSteps(when) {
			fmt.Println(hex.EncodeToString(step.Report()))
		}
		return nil
	}

	vid := uint16(protocol.VendorID)
	pid := uint16(protocol.ProductIDWired)
	if wireless {
		pid = protocol.ProductIDWireless
	}
	if vidFlag != "" {
		v, err := parseHexUint16(vidFlag)
		if err != nil {
			return fmt.Errorf("--vid: %w", err)
		}
		vid = v
	}
	if pidFlag != "" {
		p, err := parseHexUint16(pidFlag)
		if err != nil {
			return fmt.Errorf("--pid: %w", err)
		}
		pid = p
	}

	if err := hid.Init(); err != nil {
		return err
	}
	defer hid.Exit()

	dev, err := openDevice(vid, pid, path)
	if err != nil {
		return err
	}
	defer dev.Close()

	if err := setClock(dev, when); err != nil {
		return err
	}
	msg := "clock set OK"
	if test {
		msg += " (test value: 2000-01-01 23:59:59)"
	}
	fmt.Println(msg)
	return nil
}

func parseHexUint16(s string) (uint16, error) {
	v, err := strconv.ParseUint(s, 0, 16)
	if err != nil {
		return 0, err
	}
	return uint16(v), nil
}

func findCandidates(vid, pid uint16) ([]*hid.DeviceInfo, error) {
	var out []*hid.DeviceInfo
	err := hid.Enumerate(vid, pid, func(info *hid.DeviceInfo) error {
		if protocol.IsVendorUsagePage(info.UsagePage) {
			out = append(out, info)
		}
		return nil
	})
	return out, err
}

func listCandidates() error {
	if err := hid.Init(); err != nil {
		return err
	}
	defer hid.Exit()

	candidates, err := findCandidates(hid.VendorIDAny, hid.ProductIDAny)
	if err != nil {
		return err
	}
	for _, info := range candidates {
		fmt.Printf(
			"vid=0x%04x pid=0x%04x path=%q product=%q interface=%d usage_page=0x%04x\n",
			info.VendorID, info.ProductID, info.Path, info.ProductStr,
			info.InterfaceNbr, info.UsagePage,
		)
	}
	return nil
}

// probe opens path and pings it with command 0xAA. It returns the open
// device if the device answers the ping correctly, or (nil, nil) if the
// device opened but did not answer. It returns a non-nil error only if
// path could not be opened at all.
func probe(path string) (*hid.Device, error) {
	dev, err := hid.OpenPath(path)
	if err != nil {
		return nil, err
	}

	answered := func() bool {
		report := protocol.BuildReport(protocol.CmdPing, 0, nil)
		if _, err := dev.Write(report); err != nil {
			return false
		}
		reply := make([]byte, protocol.ReportSize)
		n, err := dev.ReadWithTimeout(reply, readTimeout)
		if err != nil {
			return false
		}
		return protocol.ReplyOK(reply[:n], protocol.CmdPing)
	}()
	if answered {
		return dev, nil
	}
	dev.Close()
	return nil, nil
}

func openDevice(vid, pid uint16, path string) (*hid.Device, error) {
	if path != "" {
		dev, err := probe(path)
		if err != nil {
			return nil, fmt.Errorf("open device at path %q: %w", path, err)
		}
		if dev == nil {
			return nil, fmt.Errorf("device at path %q did not answer the 0xAA ping", path)
		}
		return dev, nil
	}

	candidates, err := findCandidates(vid, pid)
	if err != nil {
		return nil, err
	}
	for _, info := range candidates {
		dev, err := probe(info.Path)
		if err != nil || dev == nil {
			continue
		}
		fmt.Fprintf(
			os.Stderr, "using %q (vid=0x%04x pid=0x%04x interface=%d)\n",
			info.ProductStr, info.VendorID, info.ProductID, info.InterfaceNbr,
		)
		return dev, nil
	}
	return nil, fmt.Errorf(
		"no HID interface answered the 0xAA ping: pass --vid/--pid/--path explicitly, " +
			"or list candidates with --list",
	)
}

func sendAndConfirm(dev *hid.Device, step protocol.Step) error {
	report := step.Report()
	if _, err := dev.Write(report); err != nil {
		return fmt.Errorf("write cmd 0x%02x offset %d: %w", step.Cmd, step.Offset, err)
	}
	reply := make([]byte, protocol.ReportSize)
	n, err := dev.ReadWithTimeout(reply, readTimeout)
	if err != nil {
		return fmt.Errorf("no valid reply for cmd 0x%02x offset %d: %w", step.Cmd, step.Offset, err)
	}
	if !protocol.ReplyOK(reply[:n], step.Cmd) {
		return fmt.Errorf("no valid reply for cmd 0x%02x offset %d", step.Cmd, step.Offset)
	}
	return nil
}

func setClock(dev *hid.Device, when time.Time) error {
	for _, step := range protocol.ClockSteps(when) {
		if err := sendAndConfirm(dev, step); err != nil {
			return err
		}
	}
	return nil
}
