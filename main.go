// The MIT License (MIT)
//
// Copyright (c) 2015, Alexandr Emelin

// Package gocent is a Go language API client for Centrifugo real-time messaging server.
//
// Usage example
//
// In example below we initialize new client with server URL address, project
// secret and request timeout. Then publish data into channel, call presence and history
// for channel and finally show how to publish several messages in one POST request to API
// endpoint using internal command buffer.
//
//  c := NewClient("http://localhost:8000", "secret", 5*time.Second)
//
//  ok, err := c.Publish("$public:chat", []byte(`{"input": "test"}`))
//  if err != nil {
//  	println(err.Error())
//  	return
//  }
//  println("Publish successful:", ok)
//
//  presence, _ := c.Presence("$public:chat")
//  fmt.Printf("Presense: %v\n", presence)
//
//  history, _ := c.History("$public:chat")
//  fmt.Printf("History: %v\n", history)
//
//  channels, _ := c.Channels()
//  fmt.Printf("Channels: %v\n", channels)
//
//  stats, _ := c.Stats()
//  fmt.Printf("Stats: %v\n", stats)
//
//  _ = c.AddPublish("$public:chat", []byte(`{"input": "test1"}`))
//  _ = c.AddPublish("$public:chat", []byte(`{"input": "test2"}`))
//  _ = c.AddPublish("$public:chat", []byte(`{"input": "test3"}`))
//
//  result, err := c.Send()
//  println("Sent", len(result), "publish commands in one request")
//
package gocent // import "github.com/scarbo87/gocent"

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	json "github.com/json-iterator/go"
	"github.com/satori/go.uuid"
	"io/ioutil"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	// ErrClientNotEmpty can be returned when client with non empty commands buffer
	// is used for single command send.
	ErrClientNotEmpty = errors.New("client command buffer not empty, send commands or reset client")
	// ErrMalformedResponse can be returned when server replied with invalid response.
	ErrMalformedResponse = errors.New("malformed response returned from server")
)

// Client is API client for project registered in server.
type Client struct {
	Endpoint string
	Secret   string
	Timeout  time.Duration

	mu       sync.RWMutex
	muCmds       sync.RWMutex
	cmds     []Command
	insecure bool
	client   *http.Client
}

// NewClient returns initialized client instance based on provided server address,
//project key, project secret, timeout and http.Transport settings
func NewClient(addr, secret string, timeout time.Duration, transport *http.Transport) *Client {

	addr = strings.TrimRight(addr, "/")
	if !strings.HasSuffix(addr, "/api") {
		addr = addr + "/api"
	}

	apiEndpoint := addr + "/"

	return &Client{
		Endpoint: apiEndpoint,
		Secret:   secret,
		Timeout:  timeout,
		cmds:     []Command{},

		client: &http.Client{
			Transport: getTransport(transport),
			Timeout:   timeout,
		},
	}
}

// NewInsecureAPIClient allows to create client that won't sign every HTTP API request.
// This is useful when your Centrifugo /api/ endpoint protected by firewall.
func NewInsecureAPIClient(addr string, timeout time.Duration, transport *http.Transport) *Client {

	addr = strings.TrimRight(addr, "/")
	if !strings.HasSuffix(addr, "/api") {
		addr = addr + "/api"
	}

	apiEndpoint := addr + "/"

	return &Client{
		Endpoint: apiEndpoint,
		Timeout:  timeout,
		cmds:     []Command{},
		insecure: true,

		client: &http.Client{
			Transport: getTransport(transport),
			Timeout:   timeout,
		},
	}
}

func getTransport(transport *http.Transport) *http.Transport {
	if transport != nil {
		return transport
	}

	defaultRoundTripper := http.DefaultTransport
	defaultTransportPointer := defaultRoundTripper.(*http.Transport)
	defaultTransport := *defaultTransportPointer // dereference it to get a copy of the struct that the pointer points to
	defaultTransport.MaxIdleConns = 1024
	defaultTransport.MaxIdleConnsPerHost = 1024
	return &defaultTransport
}

// Reset allows to clear client command buffer.
func (c *Client) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cmds = []Command{}
}

// AddPublish adds publish command to client command buffer but not actually
// send it until Send method explicitly called.
func (c *Client) AddPublish(channel string, data []byte) error {
	c.muCmds.Lock()
	defer c.muCmds.Unlock()

	var raw json.RawMessage
	raw = json.RawMessage(data)
	cmd := Command{
		Method: "publish",
		Params: map[string]interface{}{
			"channel": channel,
			"data":    &raw,
		},
	}
	return c.add(cmd)
}

// AddPublishClient adds publish command to client command buffer but not actually
// send it until Send method explicitly called.
func (c *Client) AddPublishClient(channel string, data []byte, client string) error {
	c.muCmds.Lock()
	defer c.muCmds.Unlock()

	var raw json.RawMessage
	raw = json.RawMessage(data)
	cmd := Command{
		Method: "publish",
		Params: map[string]interface{}{
			"channel": channel,
			"data":    &raw,
			"client":  client,
		},
	}
	return c.add(cmd)
}

// AddBroadcast adds broadcast command to client command buffer but not actually
// send it until Send method explicitly called.
func (c *Client) AddBroadcast(channels []string, data []byte) error {
	c.muCmds.Lock()
	defer c.muCmds.Unlock()

	var raw json.RawMessage
	raw = json.RawMessage(data)
	cmd := Command{
		Method: "broadcast",
		Params: map[string]interface{}{
			"channels": channels,
			"data":     &raw,
		},
	}
	return c.add(cmd)
}

// AddBroadcastClient adds broadcast command to client command buffer but not actually
// send it until Send method explicitly called.
func (c *Client) AddBroadcastClient(channels []string, data []byte, client string) error {
	c.muCmds.Lock()
	defer c.muCmds.Unlock()

	var raw json.RawMessage
	raw = json.RawMessage(data)
	cmd := Command{
		Method: "broadcast",
		Params: map[string]interface{}{
			"channels": channels,
			"data":     &raw,
			"client":   client,
		},
	}
	return c.add(cmd)
}

// AddUnsubscribe adds unsubscribe command to client command buffer but not actually
// send it until Send method explicitly called.
func (c *Client) AddUnsubscribe(channel string, user string) error {
	c.muCmds.Lock()
	defer c.muCmds.Unlock()

	cmd := Command{
		Method: "unsubscribe",
		Params: map[string]interface{}{
			"channel": channel,
			"user":    user,
		},
	}
	return c.add(cmd)
}

// AddDisconnect adds disconnect command to client command buffer but not actually
// send it until Send method explicitly called.
func (c *Client) AddDisconnect(user string) error {
	c.muCmds.Lock()
	defer c.muCmds.Unlock()

	cmd := Command{
		Method: "disconnect",
		Params: map[string]interface{}{
			"user": user,
		},
	}
	return c.add(cmd)
}

// AddPresence adds presence command to client command buffer but not actually
// send it until Send method explicitly called.
func (c *Client) AddPresence(channel string) error {
	c.muCmds.Lock()
	defer c.muCmds.Unlock()

	cmd := Command{
		Method: "presence",
		Params: map[string]interface{}{
			"channel": channel,
		},
	}
	return c.add(cmd)
}

// AddHistory adds history command to client command buffer but not actually
// send it until Send method explicitly called.
func (c *Client) AddHistory(channel string) error {
	c.muCmds.Lock()
	defer c.muCmds.Unlock()

	cmd := Command{
		Method: "history",
		Params: map[string]interface{}{
			"channel": channel,
		},
	}
	return c.add(cmd)
}

// AddChannels adds channels command to client command buffer but not actually
// send it until Send method explicitly called.
func (c *Client) AddChannels() error {
	c.muCmds.Lock()
	defer c.muCmds.Unlock()

	cmd := Command{
		Method: "channels",
		Params: map[string]interface{}{},
	}
	return c.add(cmd)
}

// AddStats adds stats command to client command buffer but not actually
// send it until Send method explicitly called.
func (c *Client) AddStats() error {
	c.muCmds.Lock()
	defer c.muCmds.Unlock()

	cmd := Command{
		Method: "stats",
		Params: map[string]interface{}{},
	}
	return c.add(cmd)
}

// Publish sends publish command to server and returns boolean indicator of success and
// any error occurred in process.
func (c *Client) Publish(channel string, data []byte) (bool, error) {
	if !c.empty() {
		return false, ErrClientNotEmpty
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	err := c.AddPublish(channel, data)
	if err != nil {
		return false, err
	}

	result, err := c.Send()
	if err != nil {
		return false, err
	}

	resp := result[0]
	if resp.Error != "" {
		return false, errors.New(resp.Error)
	}

	return DecodePublish(resp.Body)
}

// PublishClient sends publish command to server and returns boolean indicator of success and
// any error occurred in process. `client` is client ID initiating this event.
func (c *Client) PublishClient(channel string, data []byte, client string) (bool, error) {
	if !c.empty() {
		return false, ErrClientNotEmpty
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	err := c.AddPublishClient(channel, data, client)
	if err != nil {
		return false, err
	}

	result, err := c.Send()
	if err != nil {
		return false, err
	}

	resp := result[0]
	if resp.Error != "" {
		return false, errors.New(resp.Error)
	}

	return DecodePublish(resp.Body)
}

// Broadcast sends broadcast command to server.
func (c *Client) Broadcast(channels []string, data []byte) (bool, error) {
	if !c.empty() {
		return false, ErrClientNotEmpty
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	err := c.AddBroadcast(channels, data)
	if err != nil {
		return false, err
	}

	result, err := c.Send()
	if err != nil {
		return false, err
	}
	resp := result[0]
	if resp.Error != "" {
		return false, errors.New(resp.Error)
	}
	return DecodeBroadcast(resp.Body)
}

// BroadcastClient sends broadcast command to server with client ID.
func (c *Client) BroadcastClient(channels []string, data []byte, client string) (bool, error) {
	if !c.empty() {
		return false, ErrClientNotEmpty
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	err := c.AddBroadcastClient(channels, data, client)
	if err != nil {
		return false, err
	}

	result, err := c.Send()
	if err != nil {
		return false, err
	}
	resp := result[0]
	if resp.Error != "" {
		return false, errors.New(resp.Error)
	}
	return DecodeBroadcast(resp.Body)
}

// Unsubscribe sends unsubscribe command to server and returns boolean indicator of success and
// any error occurred in process.
func (c *Client) Unsubscribe(channel, user string) (bool, error) {
	if !c.empty() {
		return false, ErrClientNotEmpty
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	err := c.AddUnsubscribe(channel, user)
	if err != nil {
		return false, err
	}

	result, err := c.Send()
	if err != nil {
		return false, err
	}
	resp := result[0]
	if resp.Error != "" {
		return false, errors.New(resp.Error)
	}
	return DecodeUnsubscribe(resp.Body)
}

// Disconnect sends disconnect command to server and returns boolean indicator of success and
// any error occurred in process.
func (c *Client) Disconnect(user string) (bool, error) {
	if !c.empty() {
		return false, ErrClientNotEmpty
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	err := c.AddDisconnect(user)
	if err != nil {
		return false, err
	}

	result, err := c.Send()
	if err != nil {
		return false, err
	}
	resp := result[0]
	if resp.Error != "" {
		return false, errors.New(resp.Error)
	}
	return DecodeDisconnect(resp.Body)
}

// Presence sends presence command for channel to server and returns map with client
// information and any error occurred in process.
func (c *Client) Presence(channel string) (map[string]ClientInfo, error) {
	if !c.empty() {
		return map[string]ClientInfo{}, ErrClientNotEmpty
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	err := c.AddPresence(channel)
	if err != nil {
		return map[string]ClientInfo{}, err
	}

	result, err := c.Send()
	if err != nil {
		return map[string]ClientInfo{}, err
	}
	resp := result[0]
	if resp.Error != "" {
		return map[string]ClientInfo{}, errors.New(resp.Error)
	}
	return DecodePresence(resp.Body)
}

// History sends history command for channel to server and returns slice with
// messages and any error occurred in process.
func (c *Client) History(channel string) ([]Message, error) {
	if !c.empty() {
		return []Message{}, ErrClientNotEmpty
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	err := c.AddHistory(channel)
	if err != nil {
		return []Message{}, err
	}

	result, err := c.Send()
	if err != nil {
		return []Message{}, err
	}
	resp := result[0]
	if resp.Error != "" {
		return []Message{}, errors.New(resp.Error)
	}
	return DecodeHistory(resp.Body)
}

// Channels sends channels command to server and returns slice with
// active channels (with one or more subscribers).
func (c *Client) Channels() ([]string, error) {
	if !c.empty() {
		return []string{}, ErrClientNotEmpty
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	err := c.AddChannels()
	if err != nil {
		return []string{}, err
	}

	result, err := c.Send()
	if err != nil {
		return []string{}, err
	}
	resp := result[0]
	if resp.Error != "" {
		return []string{}, errors.New(resp.Error)
	}
	return DecodeChannels(resp.Body)
}

// Stats sends stats command to server and returns Stats.
func (c *Client) Stats() (Stats, error) {
	if !c.empty() {
		return Stats{}, ErrClientNotEmpty
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	err := c.AddStats()
	if err != nil {
		return Stats{}, err
	}

	result, err := c.Send()
	if err != nil {
		return Stats{}, err
	}
	resp := result[0]
	if resp.Error != "" {
		return Stats{}, errors.New(resp.Error)
	}
	return DecodeStats(resp.Body)
}

// DecodePublish allows to decode response body of publish command to get
// success flag from it. Currently no error in response means success - so nothing
// to do here yet.
func DecodePublish(body []byte) (bool, error) {
	return true, nil
}

// DecodeBroadcast allows to decode response body of broadcast command to get
// success flag from it. Currently no error in response means success - so nothing
// to do here yet.
func DecodeBroadcast(body []byte) (bool, error) {
	return true, nil
}

// DecodeUnsubscribe allows to decode response body of unsubscribe command to get
// success flag from it. Currently no error in response means success - so nothing
// to do here yet.
func DecodeUnsubscribe(body []byte) (bool, error) {
	return true, nil
}

// DecodeDisconnect allows to decode response body of disconnect command to get
// success flag from it. Currently no error in response means success - so nothing
// to do here yet.
func DecodeDisconnect(body []byte) (bool, error) {
	return true, nil
}

// DecodeHistory allows to decode history response body to get a slice of messages.
func DecodeHistory(body []byte) ([]Message, error) {
	var d historyBody
	err := json.Unmarshal(body, &d)
	if err != nil {
		return []Message{}, err
	}
	return d.Data, nil
}

// DecodeChannels allows to decode channels command response body to get a slice of channels.
func DecodeChannels(body []byte) ([]string, error) {
	var d channelsBody
	err := json.Unmarshal(body, &d)
	if err != nil {
		return []string{}, err
	}
	return d.Data, nil
}

// DecodeStats allows to decode stats command response body.
func DecodeStats(body []byte) (Stats, error) {
	var d statsBody
	err := json.Unmarshal(body, &d)
	if err != nil {
		return Stats{}, err
	}
	return d.Data, nil
}

// DecodePresence allows to decode presence response body to get a map of clients.
func DecodePresence(body []byte) (map[string]ClientInfo, error) {
	var d presenceBody
	err := json.Unmarshal(body, &d)
	if err != nil {
		return map[string]ClientInfo{}, err
	}
	return d.Data, nil
}

// Send actually makes API POST request to server sending all buffered commands in
// one request. Using this method you should manually decode all responses in
// returned Result.
func (c *Client) Send() (Result, error) {
	cmds := c.cmds
	c.cmds = []Command{}

	result, err := c.send(cmds)
	if err != nil {
		return Result{}, err
	}

	if len(result) != len(cmds) {
		return Result{}, ErrMalformedResponse
	}

	return result, nil
}

func (c *Client) send(cmds []Command) (Result, error) {
	data, err := json.Marshal(cmds)
	if err != nil {
		return Result{}, err
	}

	r, err := http.NewRequest("POST", c.Endpoint, bytes.NewBuffer(data))
	if err != nil {
		return Result{}, err
	}

	if !c.insecure {
		r.Header.Set("X-API-Sign", GenerateAPISign(c.Secret, data))
	}
	r.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(r)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Result{}, errors.New("wrong status code: " + resp.Status)
	}

	var result Result
	body, err := ioutil.ReadAll(resp.Body)
	err = json.Unmarshal(body, &result)

	return result, err
}

// Lock must be held outside this method.
// Todo: in new version uuid return error
func (c *Client) add(cmd Command) error {
	cmd.UID = uuid.NewV4().String()
	c.cmds = append(c.cmds, cmd)
	return nil
}

func (c *Client) empty() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.cmds) == 0
}

// GenerateClientToken generates client token based on secret key and provided
// connection parameters such as user ID, timestamp and info JSON string.
func GenerateClientToken(secret, user, timestamp, info string) string {
	token := hmac.New(sha256.New, []byte(secret))
	token.Write([]byte(user))
	token.Write([]byte(timestamp))
	token.Write([]byte(info))
	return hex.EncodeToString(token.Sum(nil))
}

// GenerateAPISign generates sign which is used to sign HTTP API requests.
func GenerateAPISign(secret string, data []byte) string {
	sign := hmac.New(sha256.New, []byte(secret))
	sign.Write(data)
	return hex.EncodeToString(sign.Sum(nil))
}

// GenerateChannelSign generates sign which is used to prove permission of
// client to subscribe on private channel.
func GenerateChannelSign(secret, client, channel, channelData string) string {
	sign := hmac.New(sha256.New, []byte(secret))
	sign.Write([]byte(client))
	sign.Write([]byte(channel))
	sign.Write([]byte(channelData))
	return hex.EncodeToString(sign.Sum(nil))
}
