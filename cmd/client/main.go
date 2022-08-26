// Copyright 2020 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	kingpin "gopkg.in/alecthomas/kingpin.v2"

	"github.com/Showmax/go-fqdn"
	"github.com/cenkalti/backoff/v4"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/pkg/errors"
	"github.com/prometheus-community/pushprox/util"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/promlog"
	"github.com/prometheus/common/promlog/flag"
)

var (
	myFqdn      = kingpin.Flag("fqdn", "FQDN to register with").Default(fqdn.Get()).String()
	proxyURL    = kingpin.Flag("proxy-url", "Push proxy to talk to.").Required().String()
	caCertFile  = kingpin.Flag("tls.cacert", "<file> CA certificate to verify peer against").String() // Q: isn't this authentication?
	tlsCert     = kingpin.Flag("tls.cert", "<cert> Client certificate file").String() // isn't this certification?
	tlsKey      = kingpin.Flag("tls.key", "<key> Private key file").String()
	metricsAddr = kingpin.Flag("metrics-addr", "Serve Prometheus metrics at this address").Default(":9369").String()
	connectAddr	= kingpin.Flag("connect-address", "Host address with port for HTTP connect.").String()
	localScrape	= kingpin.Flag("local-scrape", "Define to use local host as scrape target.").String()

	retryInitialWait = kingpin.Flag("proxy.retry.initial-wait", "Amount of time to wait after proxy failure").Default("1s").Duration()
	retryMaxWait     = kingpin.Flag("proxy.retry.max-wait", "Maximum amount of time to wait between proxy poll retries").Default("5s").Duration()

)

var (
	scrapeErrorCounter = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "pushprox_client_scrape_errors_total",
			Help: "Number of scrape errors",
		},
	)
	pushErrorCounter = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "pushprox_client_push_errors_total",
			Help: "Number of push errors",
		},
	)
	pollErrorCounter = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "pushprox_client_poll_errors_total",
			Help: "Number of poll errors",
		},
	)
)

func init() {
	prometheus.MustRegister(pushErrorCounter, pollErrorCounter, scrapeErrorCounter)
}

func newBackOffFromFlags() backoff.BackOff {
	b := backoff.NewExponentialBackOff()
	b.InitialInterval = *retryInitialWait
	b.Multiplier = 1.5
	b.MaxInterval = *retryMaxWait
	b.MaxElapsedTime = time.Duration(0)
	return b
}

// Coordinator for scrape requests and responses
type Coordinator struct {
	logger log.Logger
}

func (c *Coordinator) handleErr(request *http.Request, proxyClient *http.Client, err error) {
	level.Error(c.logger).Log("err", err)
	scrapeErrorCounter.Inc()
	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       ioutil.NopCloser(strings.NewReader(err.Error())),
		Header:     http.Header{},
	}
	if err = c.doPush(resp, request, proxyClient); err != nil {
		pushErrorCounter.Inc()
		level.Warn(c.logger).Log("msg", "Failed to push failed scrape response:", "err", err)
		return
	}
	level.Info(c.logger).Log("msg", "Pushed failed scrape response")
}

func (c *Coordinator) doScrape(request *http.Request, proxyClient *http.Client, scrapeTargetClient *http.Client) {
	logger := log.With(c.logger, "scrape_id", request.Header.Get("id"))
	timeout, err := util.GetHeaderTimeout(request.Header)
	if err != nil {
		c.handleErr(request, proxyClient, err) // CHECK WHICH CLIENT
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), timeout)
	defer cancel()
	request = request.WithContext(ctx)
	// We cannot handle https requests at the proxy, as we would only
	// see a CONNECT, so use a URL parameter to trigger it.
	params := request.URL.Query()
	if params.Get("_scheme") == "https" {
		request.URL.Scheme = "https"
		params.Del("_scheme")
		request.URL.RawQuery = params.Encode()
	}

	if request.URL.Hostname() != *myFqdn {
		c.handleErr(request, proxyClient, errors.New("scrape target doesn't match proxy client fqdn"))
		return
	}

	// For scraping multiple clients locally. Use "localScrape" to indicate use of localhost and differentiate between clients.
	originalHost := request.URL.Host
	if *localScrape != "" {
		portNumber := strings.Split(request.URL.Host, ":")[1]  
		request.URL.Host = "localhost:" + portNumber
	}

	scrapeResp, err := scrapeTargetClient.Do(request)
	if err != nil {
		msg := fmt.Sprintf("failed to scrape %s", request.URL.String())
		c.handleErr(request, scrapeTargetClient, errors.Wrap(err, msg))
		return
	}
	level.Info(logger).Log("msg", "Retrieved scrape response")

	if *localScrape != "" {
		request.URL.Host = originalHost
	}

	if err = c.doPush(scrapeResp, request, proxyClient); err != nil {
		pushErrorCounter.Inc()
		level.Warn(logger).Log("msg", "Failed to push scrape response:", "err", err)
		return
	}
	level.Info(logger).Log("msg", "Pushed scrape result")
}

// Report the result of the scrape back up to the proxy.
func (c *Coordinator) doPush(resp *http.Response, origRequest *http.Request, proxyClient *http.Client) error {
	resp.Header.Set("id", origRequest.Header.Get("id")) // Link the request and response
	// Remaining scrape deadline.
	deadline, _ := origRequest.Context().Deadline()
	resp.Header.Set("X-Prometheus-Scrape-Timeout", fmt.Sprintf("%f", float64(time.Until(deadline))/1e9))

	base, err := url.Parse(*proxyURL)
	if err != nil {
		return err
	}
	u, err := url.Parse("push")
	if err != nil {
		return err
	}
	url := base.ResolveReference(u)

	buf := &bytes.Buffer{}
	//nolint:errcheck // https://github.com/prometheus-community/PushProx/issues/111
	resp.Write(buf)
	request := &http.Request{
		Method:        "POST",
		URL:           url,
		Body:          ioutil.NopCloser(buf),
		ContentLength: int64(buf.Len()),
	}
	request = request.WithContext(origRequest.Context())
	if _, err = proxyClient.Do(request); err != nil {
		return err
	}
	return nil
}

func (c *Coordinator) doPoll(proxyClient *http.Client, scrapeTargetClient *http.Client) error {
	base, err := url.Parse(*proxyURL)
	if err != nil {
		level.Error(c.logger).Log("msg", "Error parsing url:", "err", err)
		return errors.Wrap(err, "error parsing url")
	}
	u, err := url.Parse("poll")
	if err != nil {
		level.Error(c.logger).Log("msg", "Error parsing url:", "err", err)
		return errors.Wrap(err, "error parsing url poll")
	}
	url := base.ResolveReference(u)
	resp, err := proxyClient.Post(url.String(), "", strings.NewReader(*myFqdn))
	if err != nil {
		level.Error(c.logger).Log("msg", "Error polling:", "err", err)
		return errors.Wrap(err, "error polling")
	}
	defer resp.Body.Close()

	request, err := http.ReadRequest(bufio.NewReader(resp.Body))
	if err != nil {
		level.Error(c.logger).Log("msg", "Error reading request:", "err", err)
		return errors.Wrap(err, "error reading request")
	}
	level.Info(c.logger).Log("msg", "Got scrape request", "scrape_id", request.Header.Get("id"), "url", request.URL)

	request.RequestURI = ""

	go c.doScrape(request, proxyClient, scrapeTargetClient)

	return nil
}

func (c *Coordinator) loop(bo backoff.BackOff, proxyClient *http.Client, scrapeTargetClient *http.Client) {
	op := func() error {
		return c.doPoll(proxyClient, scrapeTargetClient)
	}

	for {
		if err := backoff.RetryNotify(op, bo, func(err error, _ time.Duration) {
			pollErrorCounter.Inc()
		}); err != nil {
			level.Error(c.logger).Log("err", err)
		}
	}
}

func main() {
	promlogConfig := promlog.Config{}
	flag.AddFlags(kingpin.CommandLine, &promlogConfig)
	kingpin.HelpFlag.Short('h')
	kingpin.Parse()
	logger := promlog.New(&promlogConfig)
	coordinator := Coordinator{logger: logger}

	if *proxyURL == "" {
		level.Error(coordinator.logger).Log("msg", "--proxy-url flag must be specified.")
		os.Exit(1)
	}
	// Make sure proxyURL ends with a single '/'
	*proxyURL = strings.TrimRight(*proxyURL, "/") + "/"
	level.Info(coordinator.logger).Log("msg", "URL and FQDN info", "proxy_url", *proxyURL, "fqdn", *myFqdn)

	tlsConfig := &tls.Config{}
	if *tlsCert != "" {
		cert, err := tls.LoadX509KeyPair(*tlsCert, *tlsKey)
		if err != nil {
			level.Error(coordinator.logger).Log("msg", "Certificate or Key is invalid", "err", err)
			os.Exit(1)
		}

		// Setup HTTPS client
		tlsConfig.Certificates = []tls.Certificate{cert}

		tlsConfig.BuildNameToCertificate()
	}

	if *caCertFile != "" {
		caCert, err := ioutil.ReadFile(*caCertFile)
		if err != nil {
			level.Error(coordinator.logger).Log("msg", "Not able to read cacert file", "err", err)
			os.Exit(1)
		}
		caCertPool := x509.NewCertPool()
		if ok := caCertPool.AppendCertsFromPEM(caCert); !ok {
			level.Error(coordinator.logger).Log("msg", "Failed to use cacert file as ca certificate")
			os.Exit(1)
		}

		tlsConfig.RootCAs = caCertPool
	}

	if *metricsAddr != "" {
		go func() {
			if err := http.ListenAndServe(*metricsAddr, promhttp.Handler()); err != nil {
				level.Warn(coordinator.logger).Log("msg", "ListenAndServe", "err", err)
			}
		}()
	}

	var proxyTransport *http.Transport
	var scrapeTargetTransport *http.Transport
	var proxyClient *http.Client
	var scrapeTargetClient *http.Client

	if *connectAddr != "" {
		var tempErr error
	
		connectAddress := *connectAddr
		addr := strings.TrimRight(*connectAddr, "/")
		addr = strings.TrimPrefix(addr, "http://")

		dialer, tempErr := func(ctx context.Context, network, addr string) (net.Conn, error) {
			var proxyConn net.Conn
			var err error
			proxyConn, err = net.Dial("tcp", connectAddress)
			if err != nil {
				level.Error(coordinator.logger).Log("msg", "dialing proxy failed:", connectAddress, err)
				return nil, fmt.Errorf("dialing proxy failed:", connectAddress, err)
			}
			fmt.Fprintf(proxyConn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", addr, addr)
	
			br := bufio.NewReader(proxyConn)
			res, err := http.ReadResponse(br, nil)
	
			if err != nil {
				level.Error(coordinator.logger).Log("msg", "reading HTTP response from CONNECT via proxy failed",
				addr, connectAddress, err)
				return nil, fmt.Errorf("reading HTTP response from CONNECT via proxy failed", err)
			}
	
			if res.StatusCode != 200 {
				level.Error(coordinator.logger).Log("msg","proxy error from server while dialing", connectAddress, addr, res.Status)
				return nil, fmt.Errorf("proxy error from server while dialing", connectAddress, addr, res.Status)
			}
	
			return proxyConn, nil
		}, nil

		if tempErr != nil {
			level.Error(coordinator.logger).Log("msg","failed to get dialer for proxy client")
		}

		proxyTransport = &http.Transport{
			DialContext: dialer,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
		}
	} else {
		proxyTransport = &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
				DualStack: true,
			}).DialContext,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			TLSClientConfig:       tlsConfig,
		}
	}

	scrapeTargetTransport = &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
			DualStack: true,
		}).DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       tlsConfig,
	}

	proxyClient = &http.Client{Transport: proxyTransport}
	scrapeTargetClient = &http.Client{Transport: scrapeTargetTransport}

	coordinator.loop(newBackOffFromFlags(), proxyClient, scrapeTargetClient)
}