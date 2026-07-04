// Package device resolves a USB iOS device and opens a house_arrest AFC session.
package device

import (
	"fmt"

	"github.com/danielpaulus/go-ios/ios"

	"github.com/david-zw-liu/lrmount/internal/afc"
	"github.com/david-zw-liu/lrmount/internal/afcfs"
)

// vendCommands are tried in order. Lightroom is a file-sharing app
// (UIFileSharingEnabled): it answers "VendDocuments" but returns
// InstallationLookupFailed for "VendContainer". We try VendDocuments first and
// fall back to VendContainer so other apps still work.
var vendCommands = []string{"VendDocuments", "VendContainer"}

// Session is an open AFC connection to one app's container.
type Session struct {
	FS       afcfs.FS
	Label    string
	BundleID string // the bundle id that actually connected
	closer   func() error
}

func (s *Session) Close() error {
	if s.closer != nil {
		return s.closer()
	}
	return nil
}

// DescribeDevice returns a short label for a device (its udid/serial).
func DescribeDevice(d ios.DeviceEntry) string {
	return d.Properties.SerialNumber
}

// Info describes one connected USB device. It carries the usbmux DeviceEntry
// it was resolved from so DetectSessions can bind to that exact snapshot —
// avoiding a second ListDevices call that might disagree (e.g. a Wi-Fi device
// that flaps in and out of the list between calls).
type Info struct {
	UDID        string
	Name        string
	ProductType string
	Version     string
	Err         string // lockdown error (e.g. device locked), if reading values failed
	entry       ios.DeviceEntry
}

// List returns the connected USB devices, de-duplicated by udid (usbmuxd may
// report the same device over more than one transport). For each it tries a
// lockdown read for name/product/version; a failure is recorded in Err rather
// than dropping the device. Only USB-connected devices are returned; devices
// reachable only over Wi-Fi (usbmuxd "Network" transport) are ignored.
func List() ([]Info, error) {
	entries, err := usbEntries()
	if err != nil {
		return nil, err
	}
	var out []Info
	for _, d := range entries {
		info := Info{UDID: d.Properties.SerialNumber, entry: d}
		if vals, err := ios.GetValues(d); err != nil {
			info.Err = err.Error()
		} else {
			info.Name = vals.Value.DeviceName
			info.ProductType = vals.Value.ProductType
			info.Version = vals.Value.ProductVersion
		}
		out = append(out, info)
	}
	return out, nil
}

// usbEntries returns one DeviceEntry per USB-connected device (deduplicated by
// udid), skipping Wi-Fi ("Network") transports. This is a cheap usbmux query
// that never touches AFC, so it stays responsive even when a device's AFC
// connection has gone dead (e.g. the cable was pulled).
func usbEntries() ([]ios.DeviceEntry, error) {
	list, err := ios.ListDevices()
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	seen := make(map[string]bool)
	var out []ios.DeviceEntry
	for _, d := range list.DeviceList {
		if d.ConnectionTypeLabel() != "USB" {
			continue
		}
		udid := d.Properties.SerialNumber
		if seen[udid] {
			continue
		}
		seen[udid] = true
		out = append(out, d)
	}
	return out, nil
}

// openHouseArrest opens the house_arrest service and vends bundleID's
// container, trying each vend command until one succeeds.
func openHouseArrest(device ios.DeviceEntry, bundleID string) (*afc.Conn, error) {
	var lastErr error
	for _, cmd := range vendCommands {
		conn, err := afc.Vend(device, bundleID, cmd)
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("house_arrest vend for %s failed: %w", bundleID, lastErr)
}

// collectVendable probes each bundle id in order and returns a Session for
// every one that vends successfully. It errors only if none vend.
func collectVendable(bundleIDs []string, probe func(string) (*Session, error)) ([]*Session, error) {
	var out []*Session
	var lastErr error
	for _, id := range bundleIDs {
		s, err := probe(id)
		if err != nil {
			lastErr = err
			continue
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		if lastErr != nil {
			return nil, fmt.Errorf("no Lightroom app vends on this device: %w", lastErr)
		}
		return nil, fmt.Errorf("no Lightroom app found on this device")
	}
	return out, nil
}

// DetectSessions opens a house_arrest AFC session for every installed
// Lightroom app (each bundle id that vends) on the USB device described by
// info. It binds to info's snapshot DeviceEntry, so a Wi-Fi device that was
// filtered out of List never reaches here. At least one app must vend or it
// errors. Callers own Close() on every returned session.
func DetectSessions(info Info, bundleIDs []string) ([]*Session, error) {
	d := info.entry
	if d.Properties.SerialNumber == "" {
		return nil, fmt.Errorf("device %s has no USB entry", info.UDID)
	}
	return collectVendable(bundleIDs, func(id string) (*Session, error) {
		client, err := openHouseArrest(d, id)
		if err != nil {
			return nil, err
		}
		return &Session{
			FS:       afcfs.Wrap(client),
			Label:    DescribeDevice(d),
			BundleID: id,
			closer:   client.Close,
		}, nil
	})
}
