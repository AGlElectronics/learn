package files

import (
	"context"
	"net/url"
	"testing"

	"github.com/AGlElectronics/learn/backend/internal/config"
)

func TestNewDisabledWithoutEndpoint(t *testing.T) {
	c, err := New(context.Background(), &config.Config{})
	if err != nil || c != nil {
		t.Fatalf("New() = %v, %v; want nil, nil", c, err)
	}
}

func TestPublicBaseURL(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Config
		want string
	}{
		{"compose host rewritten", config.Config{S3Endpoint: "http://minio:9000"}, "http://localhost:9000"},
		{"override wins", config.Config{S3Endpoint: "http://minio:9000", S3PublicBaseURL: "https://s3.example.test"}, "https://s3.example.test"},
		{"external untouched", config.Config{S3Endpoint: "https://s3.eu.example.com"}, "https://s3.eu.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := publicBaseURL(&tt.cfg)
			if err != nil {
				t.Fatal(err)
			}
			if got := u.String(); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestRewriteURL(t *testing.T) {
	pub, _ := url.Parse("http://localhost:9020")
	c := &Client{publicBaseURL: pub}
	in, _ := url.Parse("http://minio:9000/learn-data-dev/k?X-Amz-Signature=abc")
	if got := c.rewriteURL(in).String(); got != "http://localhost:9020/learn-data-dev/k?X-Amz-Signature=abc" {
		t.Fatalf("got %q", got)
	}
}
