// Copyright 2026 The InfiniFlow Authors. All Rights Reserved.
// Licensed under the Apache License, Version 2.0.

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAnySearchRequiresOwnKey(t *testing.T) {
	for _, key := range []interface{}{nil, "", " \t\n", 123} {
		config := map[string]interface{}{"web_search_provider": "anysearch", "anysearch_api_key": key, "tavily_api_key": "other-test"}
		if resolveWebSearchProvider(config) != nil {
			t.Fatal("AnySearch accepted a missing or invalid key")
		}
	}
	provider := resolveWebSearchProvider(map[string]interface{}{"web_search_provider": "anysearch", "anysearch_api_key": "  anysearch-test ", "tavily_api_key": "other-test", "anysearch_tag": " code.doc ", "anysearch_params": map[string]interface{}{"library": "golang"}, "anysearch_extract": true})
	if provider == nil || provider.APIKey != "anysearch-test" || provider.AnySearch.Tag != "code.doc" || !provider.AnySearch.Extract {
		t.Fatalf("unexpected provider: %+v", provider)
	}
	if resolveWebSearchProvider(map[string]interface{}{"anysearch_api_key": "anysearch-test"}) != nil {
		t.Fatal("AnySearch must be opt-in")
	}
	if resolveWebSearchProvider(map[string]interface{}{"web_search_provider": "querit", "anysearch_api_key": "anysearch-test"}) != nil {
		t.Fatal("key leaked into another provider")
	}
}

func TestResolveAnySearchOptionsRejectsInvalidValues(t *testing.T) {
	for _, config := range []map[string]interface{}{
		{"anysearch_tag": "code"}, {"anysearch_tag": "code.doc&key=secret"}, {"anysearch_tag": 1},
		{"anysearch_params": "{}"}, {"anysearch_extract": "true"},
	} {
		if _, err := resolveAnySearchOptions(config); err == nil {
			t.Fatalf("accepted invalid options: %#v", config)
		}
	}
}

func TestRetrieveAnySearchRequestAndReferenceShape(t *testing.T) {
	for _, vertical := range []bool{false, true} {
		t.Run(fmt.Sprint(vertical), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/v1/search" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer anysearch-test" || r.Header.Get("Content-Type") != "application/json" {
					t.Error("missing authentication or JSON headers")
				}
				var body map[string]interface{}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["query"] != "RAGFlow?" || body["max_results"] != float64(6) {
					t.Errorf("unexpected payload: %#v", body)
				}
				if vertical && (body["tag"] != "code.doc" || body["params"].(map[string]interface{})["library"] != "golang") {
					t.Error("vertical parameters missing")
				}
				if !vertical {
					if _, exists := body["tag"]; exists {
						t.Error("automatic routing sent a tag")
					}
				}
				fmt.Fprint(w, `{"code":0,"data":{"results":[{"title":"RAGFlow","url":"https://example.com/ragflow","content":" Full text ","snippet":"ignored"},{"title":"Fallback","url":"https://example.com/snippet","content":" \t","snippet":" Summary "},{"url":"javascript:bad","content":"bad"},{"url":"https://example.com/empty","content":" "}]}}`)
			}))
			defer server.Close()
			options := anySearchOptions{}
			if vertical {
				options.Tag = "code.doc"
				options.Params = map[string]interface{}{"library": "golang"}
			}
			result, err := retrieveAnySearchWebSearch(t.Context(), server.Client(), server.URL+"/v1/search", " anysearch-test ", "RAGFlow?", options)
			if err != nil {
				t.Fatal(err)
			}
			chunks := result["chunks"].([]map[string]interface{})
			if len(chunks) != 2 || chunks[0]["chunk_id"] != "anysearch-https://example.com/ragflow" || chunks[0]["content_with_weight"] != "Full text" || chunks[1]["content_with_weight"] != "Summary" || chunks[0]["docnm_kwd"] != "RAGFlow" || chunks[0]["similarity"] != float64(1) {
				t.Fatalf("unexpected chunks: %#v", chunks)
			}
			if len(result["doc_aggs"].([]interface{})) != 2 {
				t.Fatal("missing citation aggregates")
			}
		})
	}
}

func TestAnySearchRejectsMalformedAndUnsuccessfulEnvelopes(t *testing.T) {
	for _, body := range []string{`null`, `[]`, `{}`, `{"code":null,"data":{}}`, `{"code":false,"data":{}}`, `{"code":"0","data":{}}`, `{"code":1,"message":"generated-private-test"}`, `{"code":0}`, `{"code":0,"data":[]}`, `{"code":0,"data":{"results":null}}`, `{"code":0,"data":{"results":{}}}`, `{"code":0,"data":{}}`} {
		data, err := decodeAnySearchData([]byte(body))
		if err == nil {
			_, err = decodeAnySearchHits(data)
		}
		if err == nil {
			t.Fatalf("accepted malformed envelope: %s", body)
		}
		if strings.Contains(err.Error(), "generated-private-test") {
			t.Fatal("response message leaked")
		}
	}
	data, err := decodeAnySearchData([]byte(`{"code":0,"data":{"results":[]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if hits, err := decodeAnySearchHits(data); err != nil || len(hits) != 0 {
		t.Fatal("empty success rejected")
	}
}

func TestAnySearchCapsUsableHits(t *testing.T) {
	rows := []map[string]interface{}{{"url": "https://example.com/blank", "content": " "}, {"url": "https://example.com/bad", "content": 42}}
	for i := 0; i < 10; i++ {
		rows = append(rows, map[string]interface{}{"url": fmt.Sprintf("https://example.com/%d", i), "snippet": "text"})
	}
	body, _ := json.Marshal(map[string]interface{}{"code": 0, "data": map[string]interface{}{"results": rows}})
	data, err := decodeAnySearchData(body)
	if err != nil {
		t.Fatal(err)
	}
	hits, err := decodeAnySearchHits(data)
	if err != nil || len(hits) != 6 || hits[0].URL != "https://example.com/0" {
		t.Fatalf("unexpected hits: %#v, %v", hits, err)
	}
}

func TestAnySearchHTTPFailuresDoNotLeakOrRetry(t *testing.T) {
	for _, status := range []int{400, 401, 402, 403, 429, 502} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(status)
				fmt.Fprint(w, `{"message":"password=generated-private-test api_key=anysearch-test"}`)
			}))
			defer server.Close()
			_, err := retrieveAnySearchWebSearch(t.Context(), server.Client(), server.URL, "anysearch-test", "q", anySearchOptions{})
			if err == nil || calls != 1 || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "anysearch-test") {
				t.Fatalf("unsafe failure: %v, calls=%d", err, calls)
			}
		})
	}
}

func TestAnySearchKeyGuardAndCancellation(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `{"code":0,"data":{"results":[]}}`)
	}))
	defer server.Close()
	if _, err := retrieveAnySearchWebSearch(t.Context(), server.Client(), server.URL, " ", "q", anySearchOptions{}); err == nil || calls != 0 {
		t.Fatal("blank key made a network call")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := retrieveAnySearchWebSearch(ctx, server.Client(), server.URL, "anysearch-test", "q", anySearchOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}

func TestAnySearchExtractionOptInAndFallback(t *testing.T) {
	for _, extract := range []bool{false, true} {
		t.Run(fmt.Sprint(extract), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer anysearch-test" {
					t.Error("extract lost authentication")
				}
				if r.URL.Path == "/v1/search" {
					fmt.Fprint(w, `{"code":0,"data":{"results":[{"title":"Original","url":"https://example.com/full","content":"search text"},{"url":"https://example.com/fail","snippet":"fallback"}]}}`)
					return
				}
				calls++
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["url"] == "https://example.com/fail" {
					w.WriteHeader(402)
					fmt.Fprint(w, `{"message":"private-test"}`)
					return
				}
				fmt.Fprint(w, `{"code":0,"data":{"title":"Extracted","url":"https://unrelated.example/","content":" Full body "}}`)
			}))
			defer server.Close()
			result, err := retrieveAnySearchWebSearch(t.Context(), server.Client(), server.URL+"/v1/search", "anysearch-test", "q", anySearchOptions{Extract: extract})
			if err != nil {
				t.Fatal(err)
			}
			chunks := result["chunks"].([]map[string]interface{})
			if extract && (calls != 2 || chunks[0]["content_with_weight"] != "Full body" || chunks[0]["docnm_kwd"] != "Extracted") {
				t.Fatal("extraction failed")
			}
			if !extract && (calls != 0 || chunks[0]["content_with_weight"] != "search text") {
				t.Fatal("extraction was not opt-in")
			}
			if chunks[0]["url"] != "https://example.com/full" || chunks[1]["content_with_weight"] != "fallback" {
				t.Fatal("lost provenance or fallback")
			}
		})
	}
}

func TestAnySearchSubDomainDiscoveryUsesRepeatedDomains(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer anysearch-test" {
			t.Error("invalid discovery request")
		}
		if got := r.URL.Query()["domain"]; len(got) != 2 || got[0] != "code" || got[1] != "finance" {
			t.Errorf("invalid repeated domains: %#v", got)
		}
		fmt.Fprint(w, `{"code":0,"data":{"domains":[{"domain":"code","sub_domains":[{"sub_domain":"code.doc"}]}]}}`)
	}))
	defer server.Close()
	definitions, err := listAnySearchSubDomains(t.Context(), server.Client(), server.URL, "anysearch-test", []string{" code ", " finance "})
	if err != nil || !strings.Contains(string(definitions), "code.doc") {
		t.Fatalf("discovery failed: %v", err)
	}
}

func TestAnySearchDispatchSharedByChatAndDeepResearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"code":0,"data":{"results":[]}}`) }))
	defer server.Close()
	// Rewrite the fixed provider URL in the test transport; no real network.
	previous := anySearchWebSearchHTTPClient
	anySearchWebSearchHTTPClient = &http.Client{Transport: anySearchTestTransport{server.Client().Transport, server.URL}}
	defer func() { anySearchWebSearchHTTPClient = previous }()
	provider := &webSearchProviderConfig{Provider: "anysearch", APIKey: "anysearch-test"}
	if _, err := (&ChatPipelineService{}).retrieveWebSearch(t.Context(), provider, "q"); err != nil {
		t.Fatal(err)
	}
	if _, err := (&DeepResearcher{}).retrieveWebSearch(t.Context(), provider, "q"); err != nil {
		t.Fatal(err)
	}
}

type anySearchTestTransport struct {
	base     http.RoundTripper
	endpoint string
}

func TestAnySearchResponseLimitAndTransportRedaction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, strings.Repeat("x", webSearchMaxResponseBytes+1))
	}))
	defer server.Close()
	if _, err := retrieveAnySearchWebSearch(t.Context(), server.Client(), server.URL, "anysearch-test", "q", anySearchOptions{}); err == nil {
		t.Fatal("oversized response accepted")
	}
	client := &http.Client{Transport: anySearchFailureTransport{}}
	_, err := retrieveAnySearchWebSearch(t.Context(), client, "https://api.anysearch.com/v1/search", "anysearch-test", "q", anySearchOptions{})
	if err == nil || strings.Contains(err.Error(), "private-test") || strings.Contains(err.Error(), "anysearch-test") {
		t.Fatalf("transport error leaked: %v", err)
	}
	if _, err := retrieveAnySearchWebSearch(t.Context(), client, "https://api.anysearch.com/v1/search", "anysearch-test", "  ", anySearchOptions{}); err == nil || err.Error() != "anysearch: query is required" {
		t.Fatal("blank query was sent")
	}
}

type anySearchFailureTransport struct{}

func (anySearchFailureTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("private-test anysearch-test")
}

func TestAnySearchRejectsRedirectWithoutFollowUpRequest(t *testing.T) {
	var redirectedCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedCalls.Add(1)
		fmt.Fprint(w, `{"code":0,"data":{"results":[]}}`)
	}))
	defer target.Close()
	for _, status := range []int{301, 302, 303, 307, 308} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer anysearch-test" {
					t.Error("initial request lost its key")
				}
				// A different hostname ensures this would strip credentials if followed.
				w.Header().Set("Location", strings.Replace(target.URL, "127.0.0.1", "localhost", 1))
				w.WriteHeader(status)
			}))
			defer server.Close()
			if _, err := retrieveAnySearchWebSearch(t.Context(), anySearchWebSearchHTTPClient, server.URL, "anysearch-test", "q", anySearchOptions{}); err == nil {
				t.Fatal("redirect accepted")
			}
			if redirectedCalls.Load() != 0 {
				t.Fatal("redirected request was sent")
			}
		})
	}
}

func TestAnySearchDiscoveryRejectsBlankDomainsBeforeRequest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	for _, domains := range [][]string{nil, {}, {""}, {" "}, {"code", " \t"}} {
		if _, err := listAnySearchSubDomains(t.Context(), server.Client(), server.URL, "anysearch-test", domains); err == nil {
			t.Fatalf("blank domains accepted: %#v", domains)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid discovery domains made a request")
	}
}

func (transport anySearchTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	url := *request.URL
	url.Scheme = "http"
	url.Host = strings.TrimPrefix(transport.endpoint, "http://")
	clone.URL = &url
	return transport.base.RoundTrip(clone)
}
