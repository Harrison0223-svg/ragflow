// Copyright 2026 The InfiniFlow Authors. All Rights Reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var anySearchTagPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*\.[a-z][a-z0-9_-]*$`)

type anySearchOptions struct {
	Tag     string
	Params  map[string]interface{}
	Extract bool
}

func resolveAnySearchOptions(config map[string]interface{}) (anySearchOptions, error) {
	var options anySearchOptions
	if value, exists := config["anysearch_tag"]; exists {
		tag, ok := value.(string)
		if !ok {
			return options, errors.New("anysearch: tag must be a string")
		}
		options.Tag = strings.TrimSpace(tag)
		if options.Tag != "" && !anySearchTagPattern.MatchString(options.Tag) {
			return options, errors.New("anysearch: invalid capability tag")
		}
	}
	if value, exists := config["anysearch_params"]; exists {
		params, ok := value.(map[string]interface{})
		if !ok {
			return options, errors.New("anysearch: params must be an object")
		}
		options.Params = params
	}
	if value, exists := config["anysearch_extract"]; exists {
		extract, ok := value.(bool)
		if !ok {
			return options, errors.New("anysearch: extract must be a boolean")
		}
		options.Extract = extract
	}
	return options, nil
}

// anySearchRequest shares the bounded HTTP transport, but deliberately discards
// raw upstream errors: AnySearch error messages can contain generated credentials.
// No retry or anonymous fallback is permitted for authenticated calls.
func anySearchRequest(ctx context.Context, client *http.Client, endpoint, apiKey string, payload interface{}) (map[string]json.RawMessage, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, errors.New("anysearch: API key is required")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.New("anysearch: invalid request parameters")
	}
	response, err := webSearchRequest(ctx, client, http.MethodPost, endpoint, map[string]string{
		"Authorization": "Bearer " + apiKey,
		"Accept":        "application/json",
		"Content-Type":  "application/json",
	}, bytes.NewReader(body))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("anysearch: request failed")
	}
	return decodeAnySearchData(response)
}

func decodeAnySearchData(response []byte) (map[string]json.RawMessage, error) {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(response, &envelope) != nil || envelope == nil {
		return nil, errors.New("anysearch: invalid response envelope")
	}
	var code int
	codeValue, exists := envelope["code"]
	if !exists || bytes.Equal(bytes.TrimSpace(codeValue), []byte("null")) || json.Unmarshal(codeValue, &code) != nil || code != 0 {
		return nil, errors.New("anysearch: unsuccessful or invalid response code")
	}
	var data map[string]json.RawMessage
	if json.Unmarshal(envelope["data"], &data) != nil || data == nil {
		return nil, errors.New("anysearch: invalid response data")
	}
	return data, nil
}

func isAnySearchURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Hostname() != "" && parsed.User == nil
}

func decodeAnySearchHits(data map[string]json.RawMessage) ([]webSearchHit, error) {
	var results []json.RawMessage
	value, exists := data["results"]
	if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &results) != nil {
		return nil, errors.New("anysearch: results must be an array")
	}
	hits := make([]webSearchHit, 0, len(results))
	for _, raw := range results {
		var result struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
			Snippet string `json:"snippet"`
		}
		if json.Unmarshal(raw, &result) != nil || !isAnySearchURL(result.URL) {
			continue
		}
		content := strings.TrimSpace(result.Content)
		if content == "" {
			content = strings.TrimSpace(result.Snippet)
		}
		if content == "" {
			continue
		}
		hits = append(hits, webSearchHit{Title: result.Title, URL: result.URL, Content: content})
		if len(hits) == webSearchResultCount {
			break
		}
	}
	return hits, nil
}

func retrieveAnySearchWebSearch(ctx context.Context, client *http.Client, endpoint, apiKey, query string, options anySearchOptions) (map[string]interface{}, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("anysearch: query is required")
	}
	if options.Tag != "" && !anySearchTagPattern.MatchString(options.Tag) {
		return nil, errors.New("anysearch: invalid capability tag")
	}
	payload := map[string]interface{}{"query": query, "max_results": webSearchResultCount}
	if options.Tag != "" {
		payload["tag"] = options.Tag
	}
	if options.Params != nil {
		payload["params"] = options.Params
	}
	data, err := anySearchRequest(ctx, client, endpoint, apiKey, payload)
	if err != nil {
		return nil, err
	}
	hits, err := decodeAnySearchHits(data)
	if err != nil {
		return nil, err
	}
	if options.Extract {
		// Resolve against the search endpoint so httptest can exercise both paths.
		extractURL, err := url.Parse(endpoint)
		if err != nil {
			return nil, errors.New("anysearch: invalid endpoint")
		}
		extractEndpoint := extractURL.ResolveReference(&url.URL{Path: "/v1/extract"}).String()
		for i := range hits {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			extracted, err := extractAnySearchHit(ctx, client, extractEndpoint, apiKey, hits[i])
			if err == nil {
				hits[i] = extracted
			}
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return webSearchPayload("anysearch", hits), nil
}

func extractAnySearchHit(ctx context.Context, client *http.Client, endpoint, apiKey string, hit webSearchHit) (webSearchHit, error) {
	data, err := anySearchRequest(ctx, client, endpoint, apiKey, map[string]string{"url": hit.URL})
	if err != nil {
		return hit, err
	}
	var content string
	if json.Unmarshal(data["content"], &content) != nil || strings.TrimSpace(content) == "" {
		return hit, errors.New("anysearch: extraction returned no content")
	}
	hit.Content = strings.TrimSpace(content)
	var title string
	if json.Unmarshal(data["title"], &title) == nil && strings.TrimSpace(title) != "" {
		hit.Title = title
	}
	// Preserve the searched URL as citation provenance. Extract redirects do not
	// replace it with a potentially unrelated source supplied by the provider.
	return hit, nil
}

// listAnySearchSubDomains performs discovery only when explicitly requested.
// It is never called on the chat retrieval path.
func listAnySearchSubDomains(ctx context.Context, client *http.Client, endpoint, apiKey string, domains []string) (json.RawMessage, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, errors.New("anysearch: API key is required")
	}
	if len(domains) == 0 {
		return nil, errors.New("anysearch: at least one domain is required")
	}
	parameters := url.Values{}
	for _, domain := range domains {
		parameters.Add("domain", domain)
	}
	response, err := webSearchRequest(ctx, client, http.MethodGet, endpoint+"?"+parameters.Encode(), map[string]string{
		"Authorization": "Bearer " + apiKey, "Accept": "application/json",
	}, nil)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("anysearch: discovery request failed")
	}
	data, err := decodeAnySearchData(response)
	if err != nil {
		return nil, err
	}
	var definitions []json.RawMessage
	if value := data["domains"]; bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &definitions) != nil {
		return nil, errors.New("anysearch: domains must be an array")
	}
	return data["domains"], nil
}
