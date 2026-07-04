package afc

import (
	"bytes"
	"testing"

	"github.com/danielpaulus/go-ios/ios"
)

// plistRW answers one codec-framed plist request with a canned response.
type plistRW struct {
	in   bytes.Buffer // what the client wrote
	out  *bytes.Reader
	resp map[string]interface{}
}

func newPlistRW(t *testing.T, resp map[string]interface{}) *plistRW {
	t.Helper()
	codec := ios.NewPlistCodec()
	raw, err := codec.Encode(resp)
	if err != nil {
		t.Fatal(err)
	}
	return &plistRW{out: bytes.NewReader(raw), resp: resp}
}

func (p *plistRW) Write(b []byte) (int, error) { return p.in.Write(b) }
func (p *plistRW) Read(b []byte) (int, error)  { return p.out.Read(b) }

func TestVendExchangeComplete(t *testing.T) {
	rw := newPlistRW(t, map[string]interface{}{"Status": "Complete"})
	if err := vendExchange(rw, "com.adobe.lrmobile", "VendDocuments"); err != nil {
		t.Fatal(err)
	}
	// the request must be a codec frame containing our command + identifier
	sent := rw.in.String()
	for _, want := range []string{"VendDocuments", "com.adobe.lrmobile", "Command", "Identifier"} {
		if !bytes.Contains([]byte(sent), []byte(want)) {
			t.Fatalf("request missing %q:\n%s", want, sent)
		}
	}
}

func TestVendExchangeError(t *testing.T) {
	rw := newPlistRW(t, map[string]interface{}{"Error": "InstallationLookupFailed"})
	err := vendExchange(rw, "com.example", "VendContainer")
	if err == nil || err.Error() != "InstallationLookupFailed" {
		t.Fatalf("err = %v", err)
	}
}
