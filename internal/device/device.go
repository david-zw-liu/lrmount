// Package device resolves a USB iOS device and opens a house_arrest AFC session.
package device

import (
	"fmt"

	"github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/afc"

	"github.com/davidliu/lrpush/internal/afcfs"
)

const houseArrestService = "com.apple.mobile.house_arrest"

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

// Info describes one connected device for the `devices` listing.
type Info struct {
	UDID        string
	Name        string
	ProductType string
	Version     string
	Err         string // lockdown error (e.g. device locked), if reading values failed
}

// List returns the connected USB devices, de-duplicated by udid (usbmuxd may
// report the same device over more than one transport). For each it tries a
// lockdown read for name/product/version; a failure is recorded in Err rather
// than dropping the device.
func List() ([]Info, error) {
	list, err := ios.ListDevices()
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	seen := make(map[string]bool)
	var out []Info
	for _, d := range list.DeviceList {
		udid := d.Properties.SerialNumber
		if seen[udid] {
			continue
		}
		seen[udid] = true
		info := Info{UDID: udid}
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

// Connect resolves the target device (empty udid -> first device) and opens a
// house_arrest AFC client, trying each bundleID in order until one vends
// successfully (Lightroom uses a different bundle id on iPhone vs iPad). The
// Session records which bundle id connected. At least one bundleID is required.
func Connect(udid string, bundleIDs ...string) (*Session, error) {
	d, err := ios.GetDevice(udid)
	if err != nil {
		return nil, fmt.Errorf("resolve device: %w", err)
	}
	if len(bundleIDs) == 0 {
		return nil, fmt.Errorf("no bundle id provided")
	}
	var lastErr error
	for _, bundleID := range bundleIDs {
		client, err := openHouseArrest(d, bundleID)
		if err == nil {
			return &Session{
				FS:       afcfs.Wrap(client),
				Label:    DescribeDevice(d),
				BundleID: bundleID,
				closer:   client.Close,
			}, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// openHouseArrest opens the house_arrest service and vends bundleID's container,
// trying each vend command until one succeeds.
func openHouseArrest(device ios.DeviceEntry, bundleID string) (*afc.Client, error) {
	var lastErr error
	for _, cmd := range vendCommands {
		client, err := vend(device, bundleID, cmd)
		if err == nil {
			return client, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("house_arrest vend for %s failed: %w", bundleID, lastErr)
}

// vend issues a single house_arrest vend command on a fresh service connection.
// On success the connection is owned by the returned afc.Client; on failure it
// is closed before returning.
func vend(device ios.DeviceEntry, bundleID, command string) (*afc.Client, error) {
	conn, err := ios.ConnectToService(device, houseArrestService)
	if err != nil {
		return nil, fmt.Errorf("connect house_arrest: %w", err)
	}
	codec := ios.NewPlistCodec()
	msg, err := codec.Encode(map[string]interface{}{"Command": command, "Identifier": bundleID})
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.Send(msg); err != nil {
		conn.Close()
		return nil, err
	}
	respBytes, err := codec.Decode(conn.Reader())
	if err != nil {
		conn.Close()
		return nil, err
	}
	resp, err := ios.ParsePlist(respBytes)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if status, _ := resp["Status"].(string); status == "Complete" {
		return afc.NewFromConn(conn), nil
	}
	conn.Close()
	if e, _ := resp["Error"].(string); e != "" {
		return nil, fmt.Errorf("%s: %s", command, e)
	}
	return nil, fmt.Errorf("%s: unexpected house_arrest response", command)
}
