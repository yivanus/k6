package httpext

import (
	"net"
	"net/http"
	"strconv"

	"github.com/loadimpact/k6/lib"
	"github.com/loadimpact/k6/lib/netext"
	"github.com/loadimpact/k6/stats"
	"github.com/pkg/errors"
)

// Transport is an implemenation of http.RoundTripper that will measure different metrics for each
// roundtrip
type Transport struct {
	roundTripper http.RoundTripper
	// TODO: maybe just take the SystemTags field as it is the only thing used
	options   *lib.Options
	tags      map[string]string
	trail     *Trail
	tlsInfo   netext.TLSInfo
	samplesCh chan<- stats.SampleContainer
}

var _ http.RoundTripper = &Transport{}

// NewTransport returns a new Transport wrapping around the provide Roundtripper and will send
// samples on the provided channel adding the tags in accordance to the Options provided
func NewTransport(transport http.RoundTripper, samplesCh chan<- stats.SampleContainer, options *lib.Options, tags map[string]string) *Transport {
	return &Transport{
		roundTripper: transport,
		tags:         tags,
		options:      options,
		samplesCh:    samplesCh,
	}
}

// SetOptions sets the options that should be used
func (t *Transport) SetOptions(options *lib.Options) {
	t.options = options
}

// GetTrail returns the Trail for the last request through the Transport
func (t *Transport) GetTrail() *Trail {
	return t.trail
}

// TLSInfo returns the TLSInfo of the last tls request through the transport
func (t *Transport) TLSInfo() netext.TLSInfo {
	return t.tlsInfo
}

// RoundTrip is the implementation of http.RoundTripper
func (t *Transport) RoundTrip(req *http.Request) (res *http.Response, err error) {
	if t.roundTripper == nil {
		return nil, errors.New("No roundtrip defined")
	}

	tags := map[string]string{}
	for k, v := range t.tags {
		tags[k] = v
	}

	ctx := req.Context()
	tracer := Tracer{}
	reqWithTracer := req.WithContext(WithTracer(ctx, &tracer))

	resp, err := t.roundTripper.RoundTrip(reqWithTracer)
	trail := tracer.Done()
	if err != nil {
		if t.options.SystemTags["error"] {
			tags["error"] = err.Error()
		}

		//TODO: expand/replace this so we can recognize the different non-HTTP
		// errors, probably by using a type switch for resErr
		if t.options.SystemTags["status"] {
			tags["status"] = "0"
		}
	} else {
		if t.options.SystemTags["url"] {
			tags["url"] = req.URL.String()
		}
		if t.options.SystemTags["status"] {
			tags["status"] = strconv.Itoa(resp.StatusCode)
		}
		if t.options.SystemTags["proto"] {
			tags["proto"] = resp.Proto
		}

		if resp.TLS != nil {
			tlsInfo, oscp := netext.ParseTLSConnState(resp.TLS)
			if t.options.SystemTags["tls_version"] {
				tags["tls_version"] = tlsInfo.Version
			}
			if t.options.SystemTags["ocsp_status"] {
				tags["ocsp_status"] = oscp.Status
			}

			t.tlsInfo = tlsInfo
		}
	}
	if t.options.SystemTags["ip"] && trail.ConnRemoteAddr != nil {
		if ip, _, err := net.SplitHostPort(trail.ConnRemoteAddr.String()); err == nil {
			tags["ip"] = ip
		}
	}

	t.trail = trail
	trail.SaveSamples(stats.IntoSampleTags(&tags))
	stats.PushIfNotCancelled(ctx, t.samplesCh, trail)

	return resp, err
}