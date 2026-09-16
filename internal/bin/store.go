package bin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// The two hosts chisel's bin client (support-bins) talks to. The info endpoint
// lives on APIHost; the download URL must resolve to one of these (client-side
// allow-list), so we serve the artifact from DownloadHost.
const (
	APIHost      = "api.staging.snapcraft.io"
	DownloadHost = "storage.snapcraftcontent.com"

	infoPrefix     = "/v2/bins/info/"
	downloadPrefix = "/chisel-proxy/"
)

// Store is a fake snap-store bins endpoint serving the given bins. Each bin
// carries the track/risk chisel will request for it.
type Store struct {
	bins map[string]*Bin // realname -> bin
}

// NewStore indexes bins by their (stripped) realname.
func NewStore(bins []*Bin) *Store {
	m := make(map[string]*Bin, len(bins))
	for _, b := range bins {
		m[b.Name] = b
	}
	return &Store{bins: m}
}

// Hosts are the TLS hosts the proxy must intercept for this store.
func (s *Store) Hosts() []string { return []string{APIHost, DownloadHost} }

func (s *Store) downloadURL(b *Bin) string {
	return "https://" + DownloadHost + downloadPrefix + b.Name + ".tar.xz"
}

// Handler answers the info and download endpoints. It routes by path, so it
// works for both intercepted hosts.
func (s *Store) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, infoPrefix):
			name := strings.TrimPrefix(r.URL.Path, infoPrefix)
			b, ok := s.bins[name]
			if !ok {
				http.NotFound(w, r)
				return
			}
			s.writeInfo(w, b)
		case strings.HasPrefix(r.URL.Path, downloadPrefix):
			name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, downloadPrefix), ".tar.xz")
			b, ok := s.bins[name]
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/x-xz")
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(b.TarXZ)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(b.TarXZ)
		default:
			http.NotFound(w, r)
		}
	})
}

// The JSON shapes below mirror chisel's binInfoResponse channel-map schema.
type infoResponse struct {
	Name       string       `json:"name"`
	PackageID  string       `json:"package-id"`
	ChannelMap []channelMap `json:"channel-map"`
}

type channelMap struct {
	Channel struct {
		Name     string `json:"name"`
		Risk     string `json:"risk"`
		Track    string `json:"track"`
		Platform struct {
			Architecture string `json:"architecture"`
		} `json:"platform"`
	} `json:"channel"`
	Revision struct {
		Version  string `json:"version"`
		Revision int    `json:"revision"`
		Download struct {
			URL     string `json:"url"`
			SHA3384 string `json:"sha3-384"`
			Size    int64  `json:"size"`
		} `json:"download"`
		Platforms []struct {
			Architecture string `json:"architecture"`
		} `json:"platforms"`
	} `json:"revision"`
}

func (s *Store) writeInfo(w http.ResponseWriter, b *Bin) {
	var cm channelMap
	cm.Channel.Name = b.Track + "/" + b.Risk
	cm.Channel.Risk = b.Risk
	cm.Channel.Track = b.Track
	cm.Channel.Platform.Architecture = b.Arch
	cm.Revision.Version = b.Version
	cm.Revision.Revision = 1
	cm.Revision.Download.URL = s.downloadURL(b)
	cm.Revision.Download.SHA3384 = b.SHA3384
	cm.Revision.Download.Size = int64(b.Size)
	cm.Revision.Platforms = []struct {
		Architecture string `json:"architecture"`
	}{{Architecture: b.Arch}}

	resp := infoResponse{Name: b.Name, PackageID: b.Name, ChannelMap: []channelMap{cm}}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
