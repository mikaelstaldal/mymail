package token

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mikaelstaldal/go-server-common/devicegrant"
)

func scopeIDs(scope []string) ([]int64, error) {
	ids := make([]int64, 0, len(scope))
	seen := map[int64]bool{}
	for _, entry := range scope {
		value, ok := strings.CutPrefix(entry, "folder:")
		id, err := strconv.ParseInt(value, 10, 64)
		if !ok || err != nil || id <= 0 || strconv.FormatInt(id, 10) != value || seen[id] {
			return nil, fmt.Errorf("invalid folder scope")
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("at least one folder is required")
	}
	return ids, nil
}

// DeviceGrant shares token issuance and the resource validation with Create.
func (s Store) DeviceGrant(verificationURI string) *devicegrant.Server {
	return &devicegrant.Server{
		DB: s.DB, ClientIDs: []string{"mymail-token"}, VerificationURI: verificationURI,
		ValidateScope: func(ctx context.Context, tx *sql.Tx, scope []string) error {
			ids, err := scopeIDs(scope)
			if err != nil {
				return err
			}
			for _, id := range ids {
				var exists int
				if err = tx.QueryRowContext(ctx, `SELECT 1 FROM folders WHERE id=?`, id).Scan(&exists); err != nil {
					if errors.Is(err, sql.ErrNoRows) {
						return devicegrant.ErrInvalidGrant
					}
					return err
				}
			}
			return nil
		},
		Mint: func(ctx context.Context, tx *sql.Tx, name string, scope []string, expiry time.Time) (devicegrant.Token, error) {
			ids, err := scopeIDs(scope)
			if err != nil {
				return devicegrant.Token{}, err
			}
			record, value, err := s.createTx(tx, name, expiry, ids)
			return devicegrant.Token{AccessToken: value, Slug: record.Slug}, err
		},
	}
}

// DeviceVerificationURI uses configured deployment data, never request Host.
func DeviceVerificationURI(publicURL, addr string, port int) string {
	if publicURL == "" {
		publicURL = "http://" + net.JoinHostPort(addr, strconv.Itoa(port))
	}
	u, err := url.Parse(publicURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/device-approval.html"
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func MountDeviceGrant(mux *http.ServeMux, grant *devicegrant.Server) {
	mux.HandleFunc("POST /api/v1/oauth/device_authorization", grant.Authorize)
	mux.HandleFunc("POST /api/v1/oauth/token", grant.Poll)
	mux.HandleFunc("GET /api/v1/oauth/device/{user_code}", grant.Approval)
	mux.HandleFunc("POST /api/v1/oauth/device/{user_code}", grant.Approval)
}

func IsDeviceEndpoint(r *http.Request) bool {
	return r.URL.RawPath == "" && r.Method == http.MethodPost && (r.URL.Path == "/api/v1/oauth/device_authorization" || r.URL.Path == "/api/v1/oauth/token")
}

// PublicDeviceAccess bypasses Basic only on the two CLI protocol endpoints.
// Bearer credentials must be rejected before choosing this path.
func PublicDeviceAccess(full, public http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if IsDeviceEndpoint(r) {
			scheme, _, _ := strings.Cut(r.Header.Get("Authorization"), " ")
			if strings.EqualFold(scheme, "Bearer") {
				writeError(w, 403, "token cannot access this resource")
				return
			}
			public.ServeHTTP(w, r)
		} else {
			full.ServeHTTP(w, r)
		}
	})
}
