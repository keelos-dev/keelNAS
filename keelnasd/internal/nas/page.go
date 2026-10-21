package nas

import (
	"embed"
	"encoding/base64"
	"net/http"
)

// keelNAS's page for keelOS's web (docs/04 section 16): page/page.js, the contents of the
// keelNAS tab, travels in keelnasd. keel takes it with GET /v1/page, checks the signature with
// the keys it takes packages from, and loads it into its own page: what runs in the host
// administrator's browser is what was signed at build time, whatever happened to this guest
// since. page/page.js.sig is written by build.sh (ed25519 over the file's bytes, the package
// key); a build without the key has none, and keel then shows its built-in page.
//
//go:embed page
var pageFS embed.FS

// PageReply is GET /v1/page.
type PageReply struct {
	JS  string `json:"js"`
	Sig string `json:"sig"` // base64; "" = unsigned build
}

func (s *Server) getPage(r *http.Request) (any, error) {
	js, err := pageFS.ReadFile("page/page.js")
	if err != nil {
		return nil, notFound("this keelnasd has no page")
	}
	sig, _ := pageFS.ReadFile("page/page.js.sig")
	return PageReply{JS: string(js), Sig: base64.StdEncoding.EncodeToString(sig)}, nil
}
