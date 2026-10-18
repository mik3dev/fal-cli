package download

import (
	"net/http"

	"github.com/mik3dev/fal-cli/internal/result"
)

type reqT = http.Request

func parse(u string) (*http.Request, error) { return http.NewRequest(http.MethodGet, u, nil) }

func mediaOf(u, ct string) result.Media { return result.Media{URL: u, ContentType: ct} }
