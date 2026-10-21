//go:build !devfetch

package fetch

import "net/http"

// devAllowed is false in every build but one made with -tags devfetch.
func devAllowed(string) bool { return false }

// devTransport returns the guarded transport unchanged.
func devTransport(t *http.Transport) *http.Transport { return t }
