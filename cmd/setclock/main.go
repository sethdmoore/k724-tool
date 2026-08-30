// Command setclock sets the onboard clock of a Redragon K724-RGB-PRO
// keyboard from the local time.
//
// It reads the 49-byte global settings block from the device (command 0x05),
// stamps the current time into it, and writes it back (command 0x06). Every
// other field — lighting, USB polling rate, screen config — keeps its
// on-device value, so this is safe on the wired keyboard as well as the
// wireless receiver.
package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"k724tool/internal/k724"
)

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
		vidFlag string
		pidFlag string
		path    string
		wired   bool
		list    bool
		test    bool
	)
	fs.StringVar(&vidFlag, "vid", "", "USB vendor ID override, for example 0x320f")
	fs.StringVar(&pidFlag, "pid", "", "USB product ID override, for example 0x511b")
	fs.StringVar(&path, "path", "", "exact hidapi device path")
	fs.BoolVar(&wired, "wired", false,
		"target the wired keyboard (320f:511b) instead of the wireless receiver")
	fs.BoolVar(&list, "list", false, "list candidate HID interfaces and exit")
	fs.BoolVar(&test, "test", false,
		"set an obviously fake time (2000-01-01 23:59:59) to confirm the write took effect")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if err := k724.Init(); err != nil {
		return err
	}
	defer k724.Exit()

	targets, err := k724.Enumerate()
	if err != nil {
		return err
	}

	if list {
		if len(targets) == 0 {
			fmt.Println("no K724-RGB-PRO vendor interfaces found")
		}
		for _, t := range targets {
			fmt.Printf("%-18s vid=0x%04x pid=0x%04x interface=%d path=%q product=%q\n",
				t.Label(), t.VID, t.PID, t.Iface, t.Path, t.Product)
		}
		return nil
	}

	target, err := pickTarget(targets, wired, path, vidFlag, pidFlag)
	if err != nil {
		return err
	}

	dev, err := k724.Open(target)
	if err != nil {
		return err
	}
	defer dev.Close()

	if w := dev.Firmware().Warning(); w != "" {
		fmt.Fprintln(os.Stderr, "setclock:", w)
	}

	when := time.Now()
	if test {
		when = testTime
	}
	if err := dev.SetClock(when); err != nil {
		return err
	}

	msg := "clock set OK"
	if test {
		msg += " (test value: 2000-01-01 23:59:59)"
	}
	fmt.Println(msg)
	return nil
}

// pickTarget chooses which enumerated interface to open, applying the flag
// overrides. A -path or -vid/-pid override synthesises a Target when nothing
// matching was enumerated.
func pickTarget(targets []k724.Target, wired bool, path, vidFlag, pidFlag string) (k724.Target, error) {
	want := uint16(0x511c) // wireless receiver by default
	if wired {
		want = 0x511b
	}
	if pidFlag != "" {
		p, err := parseHexUint16(pidFlag)
		if err != nil {
			return k724.Target{}, fmt.Errorf("-pid: %w", err)
		}
		want = p
	}
	vid := uint16(0x320f)
	if vidFlag != "" {
		v, err := parseHexUint16(vidFlag)
		if err != nil {
			return k724.Target{}, fmt.Errorf("-vid: %w", err)
		}
		vid = v
	}

	if path != "" {
		return k724.Target{Path: path, VID: vid, PID: want, Wired: want == 0x511b}, nil
	}

	for _, t := range targets {
		if t.VID == vid && t.PID == want {
			return t, nil
		}
	}
	if vidFlag != "" || pidFlag != "" {
		return k724.Target{VID: vid, PID: want, Wired: want == 0x511b}, nil
	}
	return k724.Target{}, fmt.Errorf(
		"no %s found (looked for %04x:%04x); try -list, or -vid/-pid/-path",
		labelFor(want), vid, want)
}

func labelFor(pid uint16) string {
	if pid == 0x511b {
		return "wired keyboard"
	}
	return "wireless receiver"
}

func parseHexUint16(s string) (uint16, error) {
	v, err := strconv.ParseUint(s, 0, 16)
	if err != nil {
		return 0, err
	}
	return uint16(v), nil
}
