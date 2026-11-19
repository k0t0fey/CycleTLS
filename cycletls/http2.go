package cycletls

import (
	"fmt"
	"strings"

	http2 "github.com/Danny-Dasilva/fhttp/http2"
)

// HTTP2Fingerprint represents an HTTP/2 client fingerprint in the Akamai format
// SETTINGS|WINDOW_UPDATE|PRIORITY|PSEUDO_HEADER_ORDER.
//
// The historical field names are kept for API compatibility: StreamDependency
// holds the connection WINDOW_UPDATE increment, Exclusive is parsed from the
// PRIORITY field and PriorityOrder holds the pseudo-header order letters.
type HTTP2Fingerprint struct {
	Settings         []http2.Setting
	StreamDependency uint32
	Exclusive        bool
	PriorityOrder    []string
}

// NewHTTP2Fingerprint creates a new HTTP2Fingerprint from string format
// Format: settings|streamDependency|exclusive|priorityOrder
// Example: "1:65536,2:0,4:6291456,6:262144|15663105|0|m,a,s,p"
func NewHTTP2Fingerprint(fingerprint string) (*HTTP2Fingerprint, error) {
	parts := strings.Split(fingerprint, "|")
	if len(parts) != 4 {
		return nil, fmt.Errorf("invalid HTTP/2 fingerprint format: expected 4 parts, got %d", len(parts))
	}

	// Parse settings
	settingsStr := parts[0]

	// Determine the separator used in the settings string
	var settingsParts []string
	if strings.Contains(settingsStr, ";") && !strings.Contains(settingsStr, ",") {
		// If settings use semicolons exclusively, split by semicolon
		settingsParts = strings.Split(settingsStr, ";")
	} else {
		// Default to comma separator
		settingsParts = strings.Split(settingsStr, ",")
	}

	settings := make([]http2.Setting, 0, len(settingsParts))

	for _, setting := range settingsParts {
		var id, val uint32
		if strings.Contains(setting, ":") {
			// Handle standard format (ID:VALUE)
			_, err := fmt.Sscanf(setting, "%d:%d", &id, &val)
			if err != nil {
				return nil, fmt.Errorf("invalid setting format: %s", setting)
			}
		} else {
			return nil, fmt.Errorf("invalid setting format: %s - expected ID:VALUE", setting)
		}
		settings = append(settings, http2.Setting{ID: http2.SettingID(id), Val: val})
	}

	// Parse stream dependency
	var streamDependency uint32
	_, err := fmt.Sscanf(parts[1], "%d", &streamDependency)
	if err != nil {
		return nil, fmt.Errorf("invalid stream dependency: %s", parts[1])
	}

	// Parse exclusive flag
	var exclusiveFlag int
	_, err = fmt.Sscanf(parts[2], "%d", &exclusiveFlag)
	if err != nil {
		return nil, fmt.Errorf("invalid exclusive flag: %s", parts[2])
	}
	exclusive := exclusiveFlag != 0

	// Parse priority order
	priorityOrder := strings.Split(parts[3], ",")

	return &HTTP2Fingerprint{
		Settings:         settings,
		StreamDependency: streamDependency,
		Exclusive:        exclusive,
		PriorityOrder:    priorityOrder,
	}, nil
}

// String returns the string representation of the HTTP/2 fingerprint
func (f *HTTP2Fingerprint) String() string {
	// Format settings
	settingStrs := make([]string, len(f.Settings))
	for i, setting := range f.Settings {
		settingStrs[i] = fmt.Sprintf("%d:%d", setting.ID, setting.Val)
	}
	settingsStr := strings.Join(settingStrs, ",")

	// Format exclusive flag
	exclusiveFlag := 0
	if f.Exclusive {
		exclusiveFlag = 1
	}

	// Format priority order
	priorityStr := strings.Join(f.PriorityOrder, ",")

	return fmt.Sprintf("%s|%d|%d|%s", settingsStr, f.StreamDependency, exclusiveFlag, priorityStr)
}

// Apply configures the HTTP/2 transport with the specified fingerprint.
//
// fhttp replaces Transport.Settings with its Navigator preset on every new
// connection, so the fingerprint is passed through Transport.HTTP2Settings,
// which fhttp writes verbatim as the initial SETTINGS frame and connection
// WINDOW_UPDATE. PRIORITY frames are not parsed: fhttp keeps the Navigator
// default for them (none for Chrome).
func (f *HTTP2Fingerprint) Apply(conn *http2.Transport) {
	settings := append([]http2.Setting(nil), f.Settings...)
	conn.Settings = settings
	conn.HTTP2Settings = &http2.HTTP2Settings{
		Settings:       settings,
		ConnectionFlow: int(f.StreamDependency),
	}
}

var pseudoHeaderNames = map[string]string{
	"m": ":method",
	"a": ":authority",
	"s": ":scheme",
	"p": ":path",
}

// pseudoHeaderOrderFromFingerprint returns the pseudo-header order from the last
// field of an HTTP/2 fingerprint, for example "m,a,s,p". ok is false when the
// fingerprint is empty or the field is not a permutation of m, a, s and p; the
// caller then keeps the order derived from the user agent.
func pseudoHeaderOrderFromFingerprint(fingerprint string) (order []string, ok bool) {
	if fingerprint == "" {
		return nil, false
	}
	parts := strings.Split(fingerprint, "|")
	if len(parts) != 4 {
		return nil, false
	}
	letters := strings.Split(parts[3], ",")
	if len(letters) != len(pseudoHeaderNames) {
		return nil, false
	}
	seen := make(map[string]bool, len(letters))
	order = make([]string, 0, len(letters))
	for _, letter := range letters {
		name, known := pseudoHeaderNames[strings.TrimSpace(letter)]
		if !known || seen[name] {
			return nil, false
		}
		seen[name] = true
		order = append(order, name)
	}
	return order, true
}

// pseudoHeaderOrder prefers the order from an explicit HTTP/2 fingerprint and
// falls back to the order derived from the user agent.
func pseudoHeaderOrder(fingerprint, userAgent string) []string {
	if order, ok := pseudoHeaderOrderFromFingerprint(fingerprint); ok {
		return order
	}
	return parseUserAgent(userAgent).HeaderOrder
}
