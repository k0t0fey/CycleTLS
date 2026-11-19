package cycletls

import (
	"bytes"
	"crypto/tls"
	"io"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	http2 "github.com/Danny-Dasilva/fhttp/http2"
	"github.com/Danny-Dasilva/fhttp/http2/hpack"
)

const (
	chrome157HTTP2Fingerprint = "1:65536;2:0;4:6291456;6:262144|15663105|0|m,a,s,p"
	chrome157UserAgent        = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"
)

type capturedHTTP2Client struct {
	settings     []http2.Setting
	windowUpdate uint32
	priorities   int
	pseudo       []string
}

// captureHTTP2Client serves one h2 connection over local TLS, records the
// client's connection preface frames and request pseudo-headers, and answers 200.
func captureHTTP2Client(t *testing.T) (string, <-chan capturedHTTP2Client) {
	t.Helper()
	certificateSource := httptest.NewUnstartedServer(nil)
	certificateSource.StartTLS()
	certificates := certificateSource.TLS.Certificates
	certificateSource.Close()

	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: certificates,
		NextProtos:   []string{"h2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	captured := make(chan capturedHTTP2Client, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

		preface := make([]byte, len(http2.ClientPreface))
		if _, err := io.ReadFull(conn, preface); err != nil || string(preface) != http2.ClientPreface {
			return
		}
		framer := http2.NewFramer(conn, conn)
		framer.ReadMetaHeaders = hpack.NewDecoder(4096, nil)
		if err := framer.WriteSettings(); err != nil {
			return
		}

		var got capturedHTTP2Client
		settingsSeen := false
		for {
			frame, err := framer.ReadFrame()
			if err != nil {
				return
			}
			switch f := frame.(type) {
			case *http2.SettingsFrame:
				if f.IsAck() || settingsSeen {
					continue
				}
				settingsSeen = true
				_ = f.ForeachSetting(func(setting http2.Setting) error {
					got.settings = append(got.settings, setting)
					return nil
				})
				if err := framer.WriteSettingsAck(); err != nil {
					return
				}
			case *http2.WindowUpdateFrame:
				if f.StreamID == 0 && got.windowUpdate == 0 {
					got.windowUpdate = f.Increment
				}
			case *http2.PriorityFrame:
				got.priorities++
			case *http2.MetaHeadersFrame:
				for _, field := range f.Fields {
					if strings.HasPrefix(field.Name, ":") {
						got.pseudo = append(got.pseudo, field.Name)
					}
				}
				var block bytes.Buffer
				encoder := hpack.NewEncoder(&block)
				_ = encoder.WriteField(hpack.HeaderField{Name: ":status", Value: "200"})
				if err := framer.WriteHeaders(http2.HeadersFrameParam{
					StreamID:      f.StreamID,
					BlockFragment: block.Bytes(),
					EndStream:     true,
					EndHeaders:    true,
				}); err != nil {
					return
				}
				captured <- got
			}
		}
	}()
	return "https://" + listener.Addr().String() + "/", captured
}

func TestHTTP2FingerprintOnWire(t *testing.T) {
	chromeOrder := []string{":method", ":authority", ":scheme", ":path"}
	cases := []struct {
		name         string
		fingerprint  string
		settings     []http2.Setting
		windowUpdate uint32
		pseudo       []string
	}{
		{
			name:        "chrome 157 fingerprint replaces the navigator preset",
			fingerprint: chrome157HTTP2Fingerprint,
			settings: []http2.Setting{
				{ID: http2.SettingHeaderTableSize, Val: 65536},
				{ID: http2.SettingEnablePush, Val: 0},
				{ID: http2.SettingInitialWindowSize, Val: 6291456},
				{ID: http2.SettingMaxHeaderListSize, Val: 262144},
			},
			windowUpdate: 15663105,
			pseudo:       chromeOrder,
		},
		{
			name:        "window update and pseudo-header order come from the fingerprint",
			fingerprint: "1:65536;4:131072;5:16384|12517377|0|m,p,a,s",
			settings: []http2.Setting{
				{ID: http2.SettingHeaderTableSize, Val: 65536},
				{ID: http2.SettingInitialWindowSize, Val: 131072},
				{ID: http2.SettingMaxFrameSize, Val: 16384},
			},
			windowUpdate: 12517377,
			pseudo:       []string{":method", ":path", ":authority", ":scheme"},
		},
		{
			name: "without a fingerprint the chrome preset is unchanged",
			settings: []http2.Setting{
				{ID: http2.SettingHeaderTableSize, Val: 65536},
				{ID: http2.SettingMaxConcurrentStreams, Val: 1000},
				{ID: http2.SettingInitialWindowSize, Val: 6291456},
				{ID: http2.SettingMaxFrameSize, Val: 16384},
				{ID: http2.SettingMaxHeaderListSize, Val: 262144},
			},
			windowUpdate: 15663105,
			pseudo:       chromeOrder,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url, captured := captureHTTP2Client(t)
			status, body := transportDispatch(t, Options{
				URL:                   url,
				Method:                "GET",
				Ja3:                   DefaultChrome_JA3,
				HTTP2Fingerprint:      tc.fingerprint,
				UserAgent:             chrome157UserAgent,
				Timeout:               2,
				InsecureSkipVerify:    true,
				EnableConnectionReuse: false,
			})
			if status != 200 {
				t.Fatalf("status %d: %s", status, body)
			}
			var got capturedHTTP2Client
			select {
			case got = <-captured:
			case <-time.After(time.Second):
				t.Fatal("server did not capture the request")
			}
			if !reflect.DeepEqual(got.settings, tc.settings) {
				t.Errorf("SETTINGS = %v, want %v", got.settings, tc.settings)
			}
			if got.windowUpdate != tc.windowUpdate {
				t.Errorf("WINDOW_UPDATE = %d, want %d", got.windowUpdate, tc.windowUpdate)
			}
			if got.priorities != 0 {
				t.Errorf("PRIORITY frames = %d, want 0", got.priorities)
			}
			if !reflect.DeepEqual(got.pseudo, tc.pseudo) {
				t.Errorf("pseudo-headers = %v, want %v", got.pseudo, tc.pseudo)
			}
		})
	}
}

func TestPseudoHeaderOrderFallsBackToUserAgent(t *testing.T) {
	firefoxUserAgent := "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:140.0) Gecko/20100101 Firefox/140.0"
	firefoxOrder := []string{":method", ":path", ":authority", ":scheme"}
	for _, fingerprint := range []string{
		"",
		"1:65536|15663105|0|m,a,s",
		"1:65536|15663105|0|m,a,s,s",
		"1:65536|15663105|0|m,a,s,x",
		"1:65536|15663105|0",
	} {
		if got := pseudoHeaderOrder(fingerprint, firefoxUserAgent); !reflect.DeepEqual(got, firefoxOrder) {
			t.Errorf("pseudoHeaderOrder(%q) = %v, want user agent order %v", fingerprint, got, firefoxOrder)
		}
	}
	got := pseudoHeaderOrder(chrome157HTTP2Fingerprint, firefoxUserAgent)
	if want := []string{":method", ":authority", ":scheme", ":path"}; !reflect.DeepEqual(got, want) {
		t.Errorf("pseudoHeaderOrder(chrome fingerprint) = %v, want %v", got, want)
	}
}
