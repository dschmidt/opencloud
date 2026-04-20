package svc

import (
	"net/http"

	"github.com/go-chi/render"
	libregraph "github.com/opencloud-eu/libre-graph-api-go"

	"github.com/opencloud-eu/opencloud/services/graph/pkg/errorcode"
)

// SearchQuery runs one or more search requests and returns the results grouped
// by request. Inspired by the MS Graph searchQuery endpoint.
func (g Graph) SearchQuery(w http.ResponseWriter, r *http.Request) {
	var req libregraph.SearchQueryRequest
	if err := StrictJSONUnmarshal(r.Body, &req); err != nil {
		g.logger.Debug().Err(err).Msg("could not decode search query request")
		errorcode.InvalidRequest.Render(w, r, http.StatusBadRequest, "invalid body schema definition")
		return
	}

	if len(req.Requests) == 0 {
		errorcode.InvalidRequest.Render(w, r, http.StatusBadRequest, "requests array must not be empty")
		return
	}

	responses := make([]libregraph.SearchResponse, 0, len(req.Requests))
	for _, sr := range req.Requests {
		total := int64(0)
		more := false
		responses = append(responses, libregraph.SearchResponse{
			SearchTerms: []string{sr.Query.QueryString},
			HitsContainers: []libregraph.SearchHitsContainer{{
				Hits:                 []libregraph.SearchHit{},
				Total:                &total,
				MoreResultsAvailable: &more,
			}},
		})
	}

	render.Status(r, http.StatusOK)
	render.JSON(w, r, libregraph.SearchQuery200Response{Value: responses})
}
