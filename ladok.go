package goladok3

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"software.sslmate.com/src/go-pkcs12"
)

// Config configures new function
type Config struct {
	Password string
	//Format       string
	URL    string
	Pkcs12 []byte
}

// Client holds the ladok object
type Client struct {
	HTTPClient     *http.Client
	format         string
	url            string
	password       string
	pkcs12         []byte
	certificate    *x509.Certificate
	chain          *x509.CertPool
	certificatePEM []byte
	privateKey     *rsa.PrivateKey
	privateKeyPEM  []byte

	Kataloginformation *kataloginformationService
	Studentinformation *studentinformationService
	Studentdeltagande  *studentdeltagandeService
	Feed               *feedService
}

// New create a new instanace of ladok
func New(config Config) (*Client, error) {
	c := &Client{
		password: config.Password,
		format:   "json",
		url:      config.URL,
		pkcs12:   config.Pkcs12,
	}

	if err := c.unwrapPkcs12(); err != nil {
		return nil, err
	}
	if err := c.httpConfigure(); err != nil {
		return nil, err
	}

	c.Studentinformation = &studentinformationService{client: c, service: "studentinformation"}
	c.Kataloginformation = &kataloginformationService{client: c, service: "kataloginformation"}
	c.Studentdeltagande = &studentdeltagandeService{client: c, service: "studentdeltagande"}
	c.Feed = &feedService{client: c, service: "feed"}

	return c, nil
}

func (c *Client) unwrapPkcs12() error {
	privateKey, clientCert, chainCerts, err := pkcs12.DecodeChain(c.pkcs12, c.password)
	if err != nil {
		return err
	}

	certPem := &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: clientCert.Raw,
	}
	keyPEM := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey.(*rsa.PrivateKey)),
	}

	c.certificate = clientCert
	c.privateKey = privateKey.(*rsa.PrivateKey)
	c.certificatePEM = pem.EncodeToMemory(certPem)
	c.privateKeyPEM = pem.EncodeToMemory(keyPEM)

	c.chain = x509.NewCertPool()
	for _, chainCert := range chainCerts {
		c.chain.AddCert(chainCert)
	}

	return nil
}

func (c *Client) httpConfigure() error {
	keyPair, err := tls.X509KeyPair(c.certificatePEM, c.privateKeyPEM)
	if err != nil {
		return err
	}

	tlsCfg := &tls.Config{
		Rand:               rand.Reader,
		Certificates:       []tls.Certificate{keyPair},
		NextProtos:         []string{},
		ClientAuth:         tls.RequireAndVerifyClientCert,
		ClientCAs:          c.chain,
		InsecureSkipVerify: false,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
		},
		PreferServerCipherSuites:    true,
		SessionTicketsDisabled:      false,
		DynamicRecordSizingDisabled: false,
		Renegotiation:               0,
		KeyLogWriter:                nil,
	}

	tlsCfg.BuildNameToCertificate()

	c.HTTPClient = &http.Client{
		Transport: &http.Transport{
			TLSClientConfig:     tlsCfg,
			DialContext:         nil,
			TLSHandshakeTimeout: 30 * time.Second,
		},
	}

	return nil
}

var ladokAcceptHeader = map[string]map[string]string{
	"studentinformation": {
		"json": "application/vnd.ladok-studentinformation+json",
		"xml":  "application/vnd.ladok-studentinformation+xml",
	},
	"studentdeltagande": {
		"json": "application/vnd.ladok-studiedeltagande+json",
		"xml":  "application/vnd.ladok-studiedeltagande+xml",
	},
	"resultat": {
		"json": "application/vnd.ladok-resultat+json",
		"xml":  "application/vnd.ladok-resultat+xml",
	},
	"uppfoljning": {
		"json": "application/vnd.ladok-uppfoljning+json",
		"xml":  "application/vnd.ladok-uppfoljning+xml",
	},
	"examen": {
		"json": "application/vnd.ladok-examen+json",
		"xml":  "application/vnd.ladok-examen+xml",
	},
	"utbildningsinformation": {
		"json": "application/vnd.ladok-utbildningsinformation+json",
		"xml":  "application/vnd.ladok-utbildningsinformation+xml",
	},
	"kataloginformation": {
		"json": "application/vnd.ladok-kataloginformation+json",
		"xml":  "application/vnd.ladok-kataloginformation+xml",
	},
	"feed": {
		"xml": "",
	},
}

// NewRequest make a new request
func (c *Client) newRequest(ctx context.Context, acceptHeader string, method, path string, body interface{}) (*http.Request, error) {
	rel, err := url.Parse(path)
	if err != nil {
		return nil, err
	}

	u, err := url.Parse(c.url)
	if err != nil {
		return nil, err
	}
	url := u.ResolveReference(rel)

	var buf io.ReadWriter
	if body != nil {
		payload := struct {
			Data interface{} `json:"data"`
		}{
			Data: body,
		}
		buf = new(bytes.Buffer)
		err = json.NewEncoder(buf).Encode(payload)
		if err != nil {
			return nil, err
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, url.String(), buf)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", acceptHeader)
	req.Header.Set("User-Agent", "goladok3/0.0.15")

	return req, nil
}

// Do does the new request
func (c *Client) do(req *http.Request, value interface{}) (*http.Response, error) {
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := checkResponse(resp); err != nil {
		buf := &bytes.Buffer{}
		if _, err := buf.ReadFrom(resp.Body); err != nil {
			return nil, err
		}
		errorReply := &Errors{}
		if err := json.Unmarshal(buf.Bytes(), errorReply); err != nil {
			return nil, err
		}
		return nil, errorReply
	}

	switch resp.Header.Get("Content-Type") {
	case contentTypeAtomXML:
		if err := xml.NewDecoder(resp.Body).Decode(value); err != nil {
			return nil, err
		}
	case contentTypeKataloginformationJSON, contentTypeStudiedeltagandeJSON, contentTypeStudentinformationJSON:
		if err := json.NewDecoder(resp.Body).Decode(value); err != nil {
			return nil, err
		}
	default:
		return nil, ErrNoValidContentType
	}

	return resp, nil
}

func checkResponse(r *http.Response) error {
	serviceName := "goladok"

	switch r.StatusCode {
	case 200, 201, 202, 204, 304:
		return nil
	case 500:
		return fmt.Errorf("%s: not allowed", serviceName)
	}
	return fmt.Errorf("%s: invalid request", serviceName)
}

func (c *Client) call(ctx context.Context, acceptHeader, method, path, param string, req, reply interface{}) (*http.Response, error) {
	request, err := c.newRequest(
		ctx,
		acceptHeader,
		method,
		fmt.Sprintf("/%s/%s", path, param),
		req,
	)
	if err != nil {
		return nil, err
	}

	resp, err := c.do(request, reply)
	if err != nil {
		return resp, err
	}

	return resp, nil
}
