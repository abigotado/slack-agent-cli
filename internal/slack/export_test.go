package slack

import "net/http"

// NewTestClient keeps arbitrary origins out of production builds.
func NewTestClient(baseURL string, httpClient *http.Client) *Client {
	return newTestClient(baseURL, httpClient)
}
