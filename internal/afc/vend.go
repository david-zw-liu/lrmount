package afc

import (
	"errors"
	"fmt"
	"io"

	"github.com/danielpaulus/go-ios/ios"
)

const houseArrestService = "com.apple.mobile.house_arrest"

// Vend opens house_arrest for bundleID with the given command
// ("VendDocuments" for file-sharing apps, "VendContainer" for the full
// sandbox) and returns an AFC conn rooted in the vended container. The same
// socket carries one plist exchange and then plain AFC.
func Vend(device ios.DeviceEntry, bundleID, command string) (*Conn, error) {
	conn, err := ios.ConnectToService(device, houseArrestService)
	if err != nil {
		return nil, fmt.Errorf("connect house_arrest: %w", err)
	}
	if err := vendExchange(conn, bundleID, command); err != nil {
		conn.Close()
		return nil, fmt.Errorf("%s %s: %w", command, bundleID, err)
	}
	return NewConn(conn), nil
}

func vendExchange(rw io.ReadWriter, bundleID, command string) error {
	codec := ios.NewPlistCodec()
	msg, err := codec.Encode(map[string]interface{}{"Command": command, "Identifier": bundleID})
	if err != nil {
		return err
	}
	if _, err := rw.Write(msg); err != nil {
		return err
	}
	respBytes, err := codec.Decode(rw)
	if err != nil {
		return err
	}
	resp, err := ios.ParsePlist(respBytes)
	if err != nil {
		return err
	}
	if status, _ := resp["Status"].(string); status == "Complete" {
		return nil
	}
	if e, _ := resp["Error"].(string); e != "" {
		return errors.New(e)
	}
	return errors.New("unexpected house_arrest response")
}
