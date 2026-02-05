package alb

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestHandler_NilPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for nil handler")
		}
	}()
	Handler(nil)
}

func TestHandler_ReturnsFunction(t *testing.T) {
	h := Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	if h == nil {
		t.Error("expected non-nil handler function")
	}
}

func TestBuildURL(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		query     map[string]string
		wantPath  string
		wantQuery url.Values
		wantErr   bool
	}{
		{
			name:     "path only",
			path:     "/api/users",
			query:    nil,
			wantPath: "/api/users",
		},
		{
			name:     "empty query map",
			path:     "/test",
			query:    map[string]string{},
			wantPath: "/test",
		},
		{
			name:      "single query param",
			path:      "/search",
			query:     map[string]string{"q": "hello"},
			wantPath:  "/search",
			wantQuery: url.Values{"q": {"hello"}},
		},
		{
			name:      "multiple query params",
			path:      "/filter",
			query:     map[string]string{"page": "1", "limit": "10"},
			wantPath:  "/filter",
			wantQuery: url.Values{"page": {"1"}, "limit": {"10"}},
		},
		{
			name:      "encoded query values",
			path:      "/search",
			query:     map[string]string{"q": "hello%20world"},
			wantPath:  "/search",
			wantQuery: url.Values{"q": {"hello world"}},
		},
		{
			name:     "root path",
			path:     "/",
			query:    nil,
			wantPath: "/",
		},
		{
			name:      "path with special chars",
			path:      "/api/v1/users%2F123",
			query:     map[string]string{"action": "view"},
			wantPath:  "/api/v1/users%2F123",
			wantQuery: url.Values{"action": {"view"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildURL(tt.path, tt.query)
			if (err != nil) != tt.wantErr {
				t.Errorf("buildURL() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil {
				return
			}
			if got.Path != tt.wantPath && got.RawPath != tt.wantPath && got.EscapedPath() != tt.wantPath {
				t.Errorf("buildURL() path = %v, want %v", got.Path, tt.wantPath)
			}
			if tt.wantQuery != nil {
				if !reflect.DeepEqual(got.Query(), tt.wantQuery) {
					t.Errorf("buildURL() query = %v, want %v", got.Query(), tt.wantQuery)
				}
			}
		})
	}
}

func TestLambdaHandler_Run(t *testing.T) {
	tests := []struct {
		name           string
		handler        http.Handler
		req            request
		wantStatus     int
		wantBody       string
		wantBodyBase64 bool
		wantHeaders    map[string]string
		wantErr        bool
	}{
		{
			name: "simple GET request",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("hello"))
			}),
			req: request{
				Method: "GET",
				Path:   "/test",
			},
			wantStatus: http.StatusOK,
			wantBody:   "hello",
		},
		{
			name: "POST with text body",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				w.Write(body)
			}),
			req: request{
				Method: "POST",
				Path:   "/echo",
				Body:   "request body",
			},
			wantStatus: http.StatusOK,
			wantBody:   "request body",
		},
		{
			name: "POST with base64 encoded body",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				w.Write(body)
			}),
			req: request{
				Method:      "POST",
				Path:        "/echo",
				Body:        base64.StdEncoding.EncodeToString([]byte("decoded content")),
				BodyEncoded: true,
			},
			wantStatus: http.StatusOK,
			wantBody:   "decoded content",
		},
		{
			name: "invalid base64 body",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("should not reach"))
			}),
			req: request{
				Method:      "POST",
				Path:        "/test",
				Body:        "not-valid-base64!!!",
				BodyEncoded: true,
			},
			wantErr: true,
		},
		{
			name: "binary response gets base64 encoded",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte{0x00, 0x01, 0x02, 0xff, 0xfe})
			}),
			req: request{
				Method: "GET",
				Path:   "/binary",
			},
			wantStatus:     http.StatusOK,
			wantBody:       base64.StdEncoding.EncodeToString([]byte{0x00, 0x01, 0x02, 0xff, 0xfe}),
			wantBodyBase64: true,
		},
		{
			name: "request headers passed to handler",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(r.Header.Get("X-Custom-Header")))
			}),
			req: request{
				Method:  "GET",
				Path:    "/headers",
				Headers: map[string]string{"X-Custom-Header": "custom-value"},
			},
			wantStatus: http.StatusOK,
			wantBody:   "custom-value",
		},
		{
			name: "host header set on request",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(r.Host))
			}),
			req: request{
				Method:  "GET",
				Path:    "/host",
				Headers: map[string]string{"Host": "example.com"},
			},
			wantStatus: http.StatusOK,
			wantBody:   "example.com",
		},
		{
			name: "query parameters in URL",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(r.URL.Query().Get("key")))
			}),
			req: request{
				Method: "GET",
				Path:   "/query",
				Query:  map[string]string{"key": "value"},
			},
			wantStatus: http.StatusOK,
			wantBody:   "value",
		},
		{
			name: "multiple query parameters",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				keys := make([]string, 0, len(q))
				for k := range q {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				var parts []string
				for _, k := range keys {
					parts = append(parts, k+"="+q.Get(k))
				}
				w.Write([]byte(strings.Join(parts, "&")))
			}),
			req: request{
				Method: "GET",
				Path:   "/query",
				Query:  map[string]string{"a": "1", "b": "2", "c": "3"},
			},
			wantStatus: http.StatusOK,
			wantBody:   "a=1&b=2&c=3",
		},
		{
			name: "response headers captured",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Response-Header", "response-value")
				w.WriteHeader(http.StatusCreated)
			}),
			req: request{
				Method: "POST",
				Path:   "/create",
			},
			wantStatus:  http.StatusCreated,
			wantHeaders: map[string]string{"X-Response-Header": "response-value"},
		},
		{
			name: "multi-value response headers joined",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Add("X-Multi", "first")
				w.Header().Add("X-Multi", "second")
				w.WriteHeader(http.StatusOK)
			}),
			req: request{
				Method: "GET",
				Path:   "/multi",
			},
			wantStatus:  http.StatusOK,
			wantHeaders: map[string]string{"X-Multi": "first,second"},
		},
		{
			name: "different status codes",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte("not found"))
			}),
			req: request{
				Method: "GET",
				Path:   "/missing",
			},
			wantStatus: http.StatusNotFound,
			wantBody:   "not found",
		},
		{
			name: "internal server error",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte("error"))
			}),
			req: request{
				Method: "GET",
				Path:   "/error",
			},
			wantStatus: http.StatusInternalServerError,
			wantBody:   "error",
		},
		{
			name: "empty body",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}),
			req: request{
				Method: "DELETE",
				Path:   "/resource",
			},
			wantStatus: http.StatusNoContent,
			wantBody:   "",
		},
		{
			name: "content length set for text body",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.ContentLength != 11 {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.WriteHeader(http.StatusOK)
			}),
			req: request{
				Method: "POST",
				Path:   "/length",
				Body:   "hello world",
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "content length set for base64 body",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.ContentLength != 7 {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.WriteHeader(http.StatusOK)
			}),
			req: request{
				Method:      "POST",
				Path:        "/length",
				Body:        base64.StdEncoding.EncodeToString([]byte("content")),
				BodyEncoded: true,
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "method passed correctly",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(r.Method))
			}),
			req: request{
				Method: "PATCH",
				Path:   "/update",
			},
			wantStatus: http.StatusOK,
			wantBody:   "PATCH",
		},
		{
			name: "path passed correctly",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(r.URL.Path))
			}),
			req: request{
				Method: "GET",
				Path:   "/api/v1/users",
			},
			wantStatus: http.StatusOK,
			wantBody:   "/api/v1/users",
		},
		{
			name: "protocol set to HTTP/1.1",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(r.Proto))
			}),
			req: request{
				Method: "GET",
				Path:   "/proto",
			},
			wantStatus: http.StatusOK,
			wantBody:   "HTTP/1.1",
		},
		{
			name: "UTF-8 response not base64 encoded",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("Hello, 世界! 🌍"))
			}),
			req: request{
				Method: "GET",
				Path:   "/utf8",
			},
			wantStatus:     http.StatusOK,
			wantBody:       "Hello, 世界! 🌍",
			wantBodyBase64: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &lambdaHandler{handler: tt.handler}
			got, err := h.Run(context.Background(), tt.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("Run() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil {
				return
			}
			if got.StatusCode != tt.wantStatus {
				t.Errorf("Run() StatusCode = %v, want %v", got.StatusCode, tt.wantStatus)
			}
			if got.Body != tt.wantBody {
				t.Errorf("Run() Body = %v, want %v", got.Body, tt.wantBody)
			}
			if got.BodyEncoded != tt.wantBodyBase64 {
				t.Errorf("Run() BodyEncoded = %v, want %v", got.BodyEncoded, tt.wantBodyBase64)
			}
			for k, v := range tt.wantHeaders {
				if got.Headers[k] != v {
					t.Errorf("Run() Header[%s] = %v, want %v", k, got.Headers[k], v)
				}
			}
		})
	}
}

func TestLambdaHandler_ContextPropagation(t *testing.T) {
	type ctxKey string
	key := ctxKey("test-key")

	h := &lambdaHandler{
		handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			val := r.Context().Value(key)
			if val == nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Write([]byte(val.(string)))
		}),
	}

	ctx := context.WithValue(context.Background(), key, "test-value")
	resp, err := h.Run(ctx, request{Method: "GET", Path: "/"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status OK, got %d", resp.StatusCode)
	}
	if resp.Body != "test-value" {
		t.Errorf("expected body 'test-value', got %q", resp.Body)
	}
}

func TestLambdaHandler_ProtoVersion(t *testing.T) {
	tests := []struct {
		name       string
		checkField string
		wantValue  interface{}
	}{
		{"ProtoMajor", "major", 1},
		{"ProtoMinor", "minor", 1},
		{"Proto", "proto", "HTTP/1.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotValue interface{}
			h := &lambdaHandler{
				handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch tt.checkField {
					case "major":
						gotValue = r.ProtoMajor
					case "minor":
						gotValue = r.ProtoMinor
					case "proto":
						gotValue = r.Proto
					}
					w.WriteHeader(http.StatusOK)
				}),
			}

			_, err := h.Run(context.Background(), request{Method: "GET", Path: "/"})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotValue != tt.wantValue {
				t.Errorf("got %v, want %v", gotValue, tt.wantValue)
			}
		})
	}
}

func TestHandler_Integration(t *testing.T) {
	tests := []struct {
		name       string
		handler    http.Handler
		req        request
		wantStatus int
		wantBody   string
	}{
		{
			name: "full integration test",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("method=" + r.Method + " path=" + r.URL.Path))
			}),
			req: request{
				Method: "GET",
				Path:   "/api/test",
			},
			wantStatus: http.StatusOK,
			wantBody:   "method=GET path=/api/test",
		},
		{
			name: "integration with query and headers",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				auth := r.Header.Get("Authorization")
				id := r.URL.Query().Get("id")
				w.Write([]byte("auth=" + auth + " id=" + id))
			}),
			req: request{
				Method:  "GET",
				Path:    "/secure",
				Query:   map[string]string{"id": "123"},
				Headers: map[string]string{"Authorization": "Bearer token"},
			},
			wantStatus: http.StatusOK,
			wantBody:   "auth=Bearer token id=123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn := Handler(tt.handler)
			resp, err := fn(context.Background(), tt.req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp.StatusCode != tt.wantStatus {
				t.Errorf("StatusCode = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
			if resp.Body != tt.wantBody {
				t.Errorf("Body = %q, want %q", resp.Body, tt.wantBody)
			}
		})
	}
}

func TestResponse_StatusDescription(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantStatus string
	}{
		{"OK", http.StatusOK, "200 OK"},
		{"Created", http.StatusCreated, "201 Created"},
		{"No Content", http.StatusNoContent, "204 No Content"},
		{"Bad Request", http.StatusBadRequest, "400 Bad Request"},
		{"Not Found", http.StatusNotFound, "404 Not Found"},
		{"Internal Server Error", http.StatusInternalServerError, "500 Internal Server Error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &lambdaHandler{
				handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(tt.statusCode)
				}),
			}
			resp, err := h.Run(context.Background(), request{Method: "GET", Path: "/"})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp.Status != tt.wantStatus {
				t.Errorf("Status = %q, want %q", resp.Status, tt.wantStatus)
			}
		})
	}
}

func TestBuildURL_QueryParameterValues(t *testing.T) {
	tests := []struct {
		name  string
		query map[string]string
		key   string
		want  string
	}{
		{
			name:  "simple value",
			query: map[string]string{"name": "john"},
			key:   "name",
			want:  "john",
		},
		{
			name:  "encoded space",
			query: map[string]string{"name": "john%20doe"},
			key:   "name",
			want:  "john doe",
		},
		{
			name:  "encoded plus",
			query: map[string]string{"query": "a%2Bb"},
			key:   "query",
			want:  "a+b",
		},
		{
			name:  "encoded ampersand",
			query: map[string]string{"company": "a%26b"},
			key:   "company",
			want:  "a&b",
		},
		{
			name:  "encoded equals",
			query: map[string]string{"expr": "x%3D5"},
			key:   "expr",
			want:  "x=5",
		},
		{
			name:  "empty value",
			query: map[string]string{"flag": ""},
			key:   "flag",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := buildURL("/test", tt.query)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := u.Query().Get(tt.key)
			if got != tt.want {
				t.Errorf("Query[%q] = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestLambdaHandler_LargeBody(t *testing.T) {
	largeBody := strings.Repeat("x", 100000)

	tests := []struct {
		name        string
		requestBody string
		encoded     bool
	}{
		{
			name:        "large text body",
			requestBody: largeBody,
			encoded:     false,
		},
		{
			name:        "large base64 body",
			requestBody: base64.StdEncoding.EncodeToString([]byte(largeBody)),
			encoded:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &lambdaHandler{
				handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, _ := io.ReadAll(r.Body)
					if len(body) != len(largeBody) {
						t.Errorf("body length = %d, want %d", len(body), len(largeBody))
					}
					w.WriteHeader(http.StatusOK)
				}),
			}

			_, err := h.Run(context.Background(), request{
				Method:      "POST",
				Path:        "/upload",
				Body:        tt.requestBody,
				BodyEncoded: tt.encoded,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestLambdaHandler_HeaderCanonicalization(t *testing.T) {
	tests := []struct {
		name       string
		headerKey  string
		headerVal  string
		lookupKey  string
		wantValue  string
	}{
		{
			name:       "lowercase header",
			headerKey:  "content-type",
			headerVal:  "application/json",
			lookupKey:  "Content-Type",
			wantValue:  "application/json",
		},
		{
			name:       "uppercase header",
			headerKey:  "CONTENT-TYPE",
			headerVal:  "text/plain",
			lookupKey:  "Content-Type",
			wantValue:  "text/plain",
		},
		{
			name:       "mixed case header",
			headerKey:  "X-Custom-Header",
			headerVal:  "custom",
			lookupKey:  "X-Custom-Header",
			wantValue:  "custom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotValue string
			h := &lambdaHandler{
				handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					gotValue = r.Header.Get(tt.lookupKey)
					w.WriteHeader(http.StatusOK)
				}),
			}

			_, err := h.Run(context.Background(), request{
				Method:  "GET",
				Path:    "/",
				Headers: map[string]string{tt.headerKey: tt.headerVal},
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotValue != tt.wantValue {
				t.Errorf("Header[%q] = %q, want %q", tt.lookupKey, gotValue, tt.wantValue)
			}
		})
	}
}

func TestLambdaHandler_EmptyRequest(t *testing.T) {
	h := &lambdaHandler{
		handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	}

	resp, err := h.Run(context.Background(), request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestLambdaHandler_ResponseHeadersInitialized(t *testing.T) {
	h := &lambdaHandler{
		handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	}

	resp, err := h.Run(context.Background(), request{Method: "GET", Path: "/"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Headers == nil {
		t.Error("expected Headers to be initialized, got nil")
	}
}
