// Package buildinfo holds the tool version and the identifiers derived from it.
package buildinfo

// Version is the getdr version. Release builds override it with
// -ldflags "-X github.com/index-pages/getdr/internal/buildinfo.Version=X.Y.Z".
var Version = "0.1.0"

const (
	// Homepage is where every link printed by the binary points.
	Homepage = "https://index-pages.com/getdr"
	// KeyHelp explains how to get a free Ahrefs API key.
	KeyHelp = Homepage + "#key"
	// Docs is the HTTP API documentation of getdr serve.
	Docs = Homepage + "#api"
)

// UserAgent is sent with every outgoing HTTP request.
func UserAgent() string { return "getdr/" + Version + " (+" + Homepage + ")" }

// ServerHeader is the Server header of getdr serve responses.
func ServerHeader() string { return "getdr/" + Version + " (index-pages.com)" }
