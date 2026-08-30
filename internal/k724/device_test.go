package k724

import "testing"

// realK724Enumeration is the exact row set `hid_enumerate` returned for a
// real K724-RGB-PRO (wired) and its 2.4 GHz receiver on one Linux machine —
// captured with a throwaway hid.Enumerate dump, kept here verbatim as the
// regression fixture for docs/MISSING_FEATURES.md "Device picker shows each
// device twice". Both the wired keyboard and the wireless receiver expose
// several HID top-level collections that are not the vendor command
// channel (keyboard, consumer control, and an unlabelled 0xffef page), and
// the wireless receiver additionally exposes the *confirmed* vendor page
// (protocol.UsagePageVendor, 0xff1c — docs/PROTOCOL.md) on two different
// USB interfaces of the same physical unit.
var realK724Enumeration = []enumInfo{
	// Wired keyboard, 320f:511b.
	{Path: "/dev/hidraw6", VendorID: 0x320f, ProductID: 0x511b, InterfaceNbr: 0, UsagePage: 0x0001, ProductStr: "Gaming KB"},
	{Path: "/dev/hidraw7", VendorID: 0x320f, ProductID: 0x511b, InterfaceNbr: 1, UsagePage: 0x0001, ProductStr: "Gaming KB"},
	{Path: "/dev/hidraw7", VendorID: 0x320f, ProductID: 0x511b, InterfaceNbr: 1, UsagePage: 0x0001, ProductStr: "Gaming KB"},
	{Path: "/dev/hidraw7", VendorID: 0x320f, ProductID: 0x511b, InterfaceNbr: 1, UsagePage: 0x000c, ProductStr: "Gaming KB"},
	{Path: "/dev/hidraw7", VendorID: 0x320f, ProductID: 0x511b, InterfaceNbr: 1, UsagePage: 0xff1c, ProductStr: "Gaming KB"},
	{Path: "/dev/hidraw7", VendorID: 0x320f, ProductID: 0x511b, InterfaceNbr: 1, UsagePage: 0x0001, ProductStr: "Gaming KB"},
	{Path: "/dev/hidraw7", VendorID: 0x320f, ProductID: 0x511b, InterfaceNbr: 1, UsagePage: 0x0001, ProductStr: "Gaming KB"},
	{Path: "/dev/hidraw8", VendorID: 0x320f, ProductID: 0x511b, InterfaceNbr: 2, UsagePage: 0xffef, ProductStr: "Gaming KB"},
	// Wireless receiver, 320f:511c.
	{Path: "/dev/hidraw0", VendorID: 0x320f, ProductID: 0x511c, InterfaceNbr: 0, UsagePage: 0x0001, ProductStr: "2.4G Wireless Receiver"},
	{Path: "/dev/hidraw1", VendorID: 0x320f, ProductID: 0x511c, InterfaceNbr: 1, UsagePage: 0x0001, ProductStr: "2.4G Wireless Receiver"},
	{Path: "/dev/hidraw1", VendorID: 0x320f, ProductID: 0x511c, InterfaceNbr: 1, UsagePage: 0x0001, ProductStr: "2.4G Wireless Receiver"},
	{Path: "/dev/hidraw1", VendorID: 0x320f, ProductID: 0x511c, InterfaceNbr: 1, UsagePage: 0x000c, ProductStr: "2.4G Wireless Receiver"},
	{Path: "/dev/hidraw1", VendorID: 0x320f, ProductID: 0x511c, InterfaceNbr: 1, UsagePage: 0xff1c, ProductStr: "2.4G Wireless Receiver"},
	{Path: "/dev/hidraw1", VendorID: 0x320f, ProductID: 0x511c, InterfaceNbr: 1, UsagePage: 0xffef, ProductStr: "2.4G Wireless Receiver"},
	{Path: "/dev/hidraw1", VendorID: 0x320f, ProductID: 0x511c, InterfaceNbr: 1, UsagePage: 0x0001, ProductStr: "2.4G Wireless Receiver"},
	{Path: "/dev/hidraw2", VendorID: 0x320f, ProductID: 0x511c, InterfaceNbr: 2, UsagePage: 0xff1c, ProductStr: "2.4G Wireless Receiver"},
}

func TestBuildTargetsDedupesRealHardwareCapture(t *testing.T) {
	out := buildTargets(realK724Enumeration)

	if len(out) != 2 {
		t.Fatalf("buildTargets returned %d target(s), want 2 (one wired, one wireless): %+v", len(out), out)
	}

	wired, wireless := out[0], out[1]
	if !wired.Wired || wired.PID != 0x511b {
		t.Errorf("out[0] = %+v, want the wired keyboard", wired)
	}
	if wireless.Wired || wireless.PID != 0x511c {
		t.Errorf("out[1] = %+v, want the wireless receiver", wireless)
	}

	// Each representative Target must actually be on the confirmed vendor
	// usage page's interface, not one of the incidental non-command
	// collections that share its path/interface number.
	if wired.Path != "/dev/hidraw7" || wired.Iface != 1 {
		t.Errorf("wired target = %+v, want the 0xff1c interface (/dev/hidraw7, iface 1)", wired)
	}
	if wireless.Path != "/dev/hidraw1" || wireless.Iface != 1 {
		t.Errorf("wireless target = %+v, want the first 0xff1c interface (/dev/hidraw1, iface 1)", wireless)
	}
}

func TestBuildTargetsIgnoresUnrelatedProducts(t *testing.T) {
	infos := []enumInfo{
		{Path: "/dev/hidraw9", VendorID: 0x320f, ProductID: 0x9999, UsagePage: 0xff1c},
		{Path: "/dev/hidraw3", VendorID: 0x046d, ProductID: 0xc095, UsagePage: 0xff1c},
	}
	if out := buildTargets(infos); len(out) != 0 {
		t.Errorf("buildTargets(%+v) = %+v, want no targets", infos, out)
	}
}
