// Package alb provides adapter enabling usage of http.Handler inside AWS Lambda
// running behind AWS ALB as described here:
// https://docs.aws.amazon.com/lambda/latest/dg/services-alb.html
//
// Usage example:
//
//	package main
//
//	import (
//		"fmt"
//		"net/http"
//
//		"github.com/MichaelFraser99/alb"
//		"github.com/aws/aws-lambda-go/lambda"
//	)
//
//	func main() { lambda.Start(alb.Handler(http.HandlerFunc(hello))) }
//
//	func hello(w http.ResponseWriter, r *http.Request) {
//		fmt.Fprintln(w, "Hello from AWS Lambda behind ALB")
//	}
//
// Note: since both request and reply to/from AWS Lambda are passed as
// json-encoded payloads, their sizes are limited. AWS documentation states
// that: "The maximum size of the request body that you can send to a Lambda
// function is 1 MB. [...] The maximum size of the response JSON that the Lambda
// function can send is 1 MB." The exact limit of response size also depends on
// whether its body is valid utf8 or not, as non-utf8 payloads are transparently
// base64-encoded, which adds some overhead.
//
// For further details see
// https://docs.aws.amazon.com/elasticloadbalancing/latest/application/lambda-functions.html
package alb

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"strings"
	"unicode/utf8"
)

// Handler returns a function suitable to use as an AWS Lambda handler with
// github.com/aws/aws-lambda-go/lambda package.
//
// Note that the request is fully cached in memory.
func Handler(h http.Handler) func(context.Context, request) (*response, error) {
	if h == nil {
		panic("Wrap called with nil handler")
	}
	hh := lambdaHandler{handler: h}
	return hh.Run
}

type request struct {
	Method            string              `json:"httpMethod"`
	Path              string              `json:"path"`
	Query             map[string]string   `json:"queryStringParameters"`
	MultiValueQuery   map[string][]string `json:"multiValueQueryStringParameters"`
	Headers           map[string]string   `json:"headers"`
	MultiValueHeaders map[string][]string `json:"multiValueHeaders"`
	Body              string              `json:"body"`
	BodyEncoded       bool                `json:"isBase64Encoded"`
}

func (r *request) HeadersProvided() map[string][]string {
	if r.MultiValueHeaders == nil {
		container := make(map[string][]string, len(r.Headers))
		for k, v := range r.Headers {
			container[k] = []string{v}
		}
		return container
	}
	return r.MultiValueHeaders
}

func (r *request) QueryProvided() map[string][]string {
	if r.MultiValueQuery == nil {
		container := make(map[string][]string, len(r.Query))
		for k, v := range r.Query {
			container[k] = []string{v}
		}
		return container
	}
	return r.MultiValueQuery
}

type response struct {
	StatusCode        int                 `json:"statusCode"`
	Status            string              `json:"statusDescription"`
	Headers           map[string]string   `json:"headers"`
	MultiValueHeaders map[string][]string `json:"multiValueHeaders"`
	Body              string              `json:"body"`
	BodyEncoded       bool                `json:"isBase64Encoded"`
}

func (r *response) SetHeaders(req *request, res *http.Response) {
	if req.MultiValueHeaders == nil {
		r.Headers = make(map[string]string, len(res.Header))
		for k, vv := range res.Header {
			r.Headers[k] = strings.Join(vv, ",")
		}
	} else {
		r.MultiValueHeaders = res.Header
	}
}

type lambdaHandler struct {
	handler http.Handler
}

func (h *lambdaHandler) Run(ctx context.Context, req request) (*response, error) {
	u, err := buildURL(req.Path, req.QueryProvided())
	if err != nil {
		return nil, err
	}

	headers := make(http.Header, len(req.Headers))
	for k, v := range req.HeadersProvided() {
		headers[textproto.CanonicalMIMEHeaderKey(k)] = v
	}
	r := &http.Request{
		ProtoMajor: 1,
		ProtoMinor: 1,
		Proto:      "HTTP/1.1",
		Method:     req.Method,
		URL:        u,
		Header:     headers,
		Host:       headers.Get("Host"),
	}
	r = r.WithContext(ctx)
	switch {
	case req.BodyEncoded:
		b, err := base64.StdEncoding.DecodeString(req.Body)
		if err != nil {
			return nil, err
		}
		r.Body = io.NopCloser(bytes.NewReader(b))
		r.ContentLength = int64(len(b))
	default:
		r.Body = io.NopCloser(strings.NewReader(req.Body))
		r.ContentLength = int64(len(req.Body))
	}
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, r)
	res := recorder.Result()
	out := &response{
		StatusCode: res.StatusCode,
		Status:     res.Status,
	}
	out.SetHeaders(&req, res)
	if b := recorder.Body.Bytes(); utf8.Valid(b) {
		out.Body = recorder.Body.String()
	} else {
		out.Body = base64.StdEncoding.EncodeToString(b)
		out.BodyEncoded = true
	}
	return out, nil
}

// buildURL constructs url from already escaped path and query string parameters
// minimizing allocations and escaping overhead.
func buildURL(path string, query map[string][]string) (*url.URL, error) {
	if len(query) == 0 {
		return url.Parse(path)
	}
	var b strings.Builder
	b.WriteString(path)
	b.WriteByte('?')
	var i int
	for k, v := range query {
		for _, vv := range v {
			if i != 0 {
				b.WriteByte('&')
			}
			b.WriteString(k)
			b.WriteByte('=')
			b.WriteString(vv)
			i++
		}
	}
	return url.Parse(b.String())
}
