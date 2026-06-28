package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
	void* ptr;
	size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);

typedef struct {
	uint32_t abi_version;
	void* host_ctx;
	cliproxy_host_call_fn call;
	cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct {
	uint32_t abi_version;
	cliproxy_plugin_call_fn call;
	cliproxy_plugin_free_fn free_buffer;
	cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);

static const cliproxy_host_api* stored_host;

static void store_host_api(const cliproxy_host_api* host) {
	stored_host = host;
}

static int call_host_api(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
	if (stored_host == NULL || stored_host->call == NULL) {
		return 1;
	}
	return stored_host->call(stored_host->host_ctx, method, request, request_len, response);
}

static void free_host_buffer(void* ptr, size_t len) {
	if (stored_host != NULL && stored_host->free_buffer != NULL && ptr != NULL) {
		stored_host->free_buffer(ptr, len);
	}
}
*/
import "C"

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
	"unsafe"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}

type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type registration struct {
	SchemaVersion uint32                 `json:"schema_version"`
	Metadata      pluginapi.Metadata     `json:"metadata"`
	Capabilities  registrationCapability `json:"capabilities"`
}

type registrationCapability struct {
	ModelProvider         bool                         `json:"model_provider"`
	AuthProvider          bool                         `json:"auth_provider"`
	Executor              bool                         `json:"executor"`
	ExecutorModelScope    pluginapi.ExecutorModelScope `json:"executor_model_scope"`
	ExecutorInputFormats  []string                     `json:"executor_input_formats,omitempty"`
	ExecutorOutputFormats []string                     `json:"executor_output_formats,omitempty"`
}

type identifierResponse struct {
	Identifier string `json:"identifier"`
}

type rpcAuthLoginStartRequest struct {
	pluginapi.AuthLoginStartRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type rpcExecutorRequest struct {
	pluginapi.ExecutorRequest
	StreamID       string `json:"stream_id,omitempty"`
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type rpcExecutorStreamResponse struct {
	Headers http.Header                     `json:"headers,omitempty"`
	Chunks  []pluginapi.ExecutorStreamChunk `json:"chunks,omitempty"`
}

type rpcHostHTTPRequest struct {
	HostCallbackID string      `json:"host_callback_id,omitempty"`
	Method         string      `json:"method,omitempty"`
	URL            string      `json:"url,omitempty"`
	Headers        http.Header `json:"headers,omitempty"`
	Body           []byte      `json:"body,omitempty"`
}

type rpcHostHTTPStreamResponse struct {
	StatusCode int         `json:"status_code"`
	Headers    http.Header `json:"headers,omitempty"`
	StreamID   string      `json:"stream_id,omitempty"`
}

type rpcHostHTTPStreamReadRequest struct {
	StreamID string `json:"stream_id"`
}

type rpcHostHTTPStreamReadResponse struct {
	Payload []byte `json:"payload,omitempty"`
	Error   string `json:"error,omitempty"`
	Done    bool   `json:"done,omitempty"`
}

type rpcHostHTTPStreamCloseRequest struct {
	StreamID string `json:"stream_id"`
}

type rpcStreamEmitRequest struct {
	StreamID string `json:"stream_id"`
	Payload  []byte `json:"payload,omitempty"`
	Error    string `json:"error,omitempty"`
}

type rpcStreamCloseRequest struct {
	StreamID string `json:"stream_id"`
	Error    string `json:"error,omitempty"`
}

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return 1
	}
	C.store_host_api(host)
	plugin.abi_version = C.uint32_t(pluginabi.ABIVersion)
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeResponse(response, errorEnvelope("invalid_method", "method is required"))
		return 1
	}
	var requestBytes []byte
	if request != nil && requestLen > 0 {
		requestBytes = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	raw, errHandle := handleMethod(C.GoString(method), requestBytes)
	if errHandle != nil {
		writeResponse(response, errorEnvelope("plugin_error", errHandle.Error()))
		return 1
	}
	writeResponse(response, raw)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, len C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
	_ = len
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {}

func handleMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		return okEnvelope(pluginRegistration())
	case pluginabi.MethodAuthIdentifier, pluginabi.MethodExecutorIdentifier:
		return okEnvelope(identifierResponse{Identifier: providerID})
	case pluginabi.MethodAuthParse:
		return handleAuthParse(request)
	case pluginabi.MethodAuthLoginStart:
		return handleAuthLoginStart(request)
	case pluginabi.MethodAuthLoginPoll:
		return handleAuthLoginPoll(request)
	case pluginabi.MethodAuthRefresh:
		return handleAuthRefresh(request)
	case pluginabi.MethodModelStatic, pluginabi.MethodModelForAuth:
		return okEnvelope(pluginapi.ModelResponse{Provider: providerID, Models: defaultCommandCodeModels()})
	case pluginabi.MethodExecutorExecute:
		return handleExecutorExecute(request)
	case pluginabi.MethodExecutorExecuteStream:
		return handleExecutorExecuteStream(request)
	case pluginabi.MethodExecutorCountTokens:
		return okEnvelope(pluginapi.ExecutorResponse{Payload: []byte(`{"total_tokens":0}`)})
	case pluginabi.MethodExecutorHTTPRequest:
		return handleExecutorHTTPRequest(request)
	default:
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}

func pluginRegistration() registration {
	return registration{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata: pluginapi.Metadata{
			Name:             "commandcode",
			Version:          "0.1.0",
			Author:           "router-for-me",
			GitHubRepository: "https://github.com/router-for-me/CLIProxyAPI",
			Logo:             "https://raw.githubusercontent.com/router-for-me/CLIProxyAPI/main/docs/logo.png",
			ConfigFields:     []pluginapi.ConfigField{},
		},
		Capabilities: registrationCapability{
			ModelProvider:         true,
			AuthProvider:          true,
			Executor:              true,
			ExecutorModelScope:    pluginapi.ExecutorModelScopeOAuth,
			ExecutorInputFormats:  []string{"chat-completions"},
			ExecutorOutputFormats: []string{"chat-completions"},
		},
	}
}

func handleAuthParse(raw []byte) ([]byte, error) {
	var req pluginapi.AuthParseRequest
	if errUnmarshal := json.Unmarshal(raw, &req); errUnmarshal != nil {
		return nil, errUnmarshal
	}
	resp, errParse := parseCommandCodeAuth(req.RawJSON)
	if errParse != nil {
		return nil, errParse
	}
	return okEnvelope(resp)
}

func handleAuthLoginStart(raw []byte) ([]byte, error) {
	var req rpcAuthLoginStartRequest
	if errUnmarshal := json.Unmarshal(raw, &req); errUnmarshal != nil {
		return nil, errUnmarshal
	}
	resp, errStart := startCommandCodeLogin(req.AuthLoginStartRequest, randomOAuthState, time.Now().UTC())
	if errStart != nil {
		return nil, errStart
	}
	return okEnvelope(resp)
}

func handleAuthLoginPoll(raw []byte) ([]byte, error) {
	var req pluginapi.AuthLoginPollRequest
	if errUnmarshal := json.Unmarshal(raw, &req); errUnmarshal != nil {
		return nil, errUnmarshal
	}
	resp, errPoll := pollCommandCodeLogin(req)
	if errPoll != nil {
		return nil, errPoll
	}
	return okEnvelope(resp)
}

func handleAuthRefresh(raw []byte) ([]byte, error) {
	var req pluginapi.AuthRefreshRequest
	if errUnmarshal := json.Unmarshal(raw, &req); errUnmarshal != nil {
		return nil, errUnmarshal
	}
	auth, errAuth := commandCodeAuthDataFromRaw(req.StorageJSON)
	if errAuth != nil {
		return nil, errAuth
	}
	return okEnvelope(pluginapi.AuthRefreshResponse{Auth: auth})
}

func handleExecutorExecute(raw []byte) ([]byte, error) {
	var req rpcExecutorRequest
	if errUnmarshal := json.Unmarshal(raw, &req); errUnmarshal != nil {
		return nil, errUnmarshal
	}
	upstreamReq, errBuild := buildCommandCodeHTTPRequest(req.ExecutorRequest, commandCodeConfig{}, "", time.Now().UTC().Format(time.DateOnly))
	if errBuild != nil {
		return nil, errBuild
	}
	resp, errDo := hostHTTPDo(req.HostCallbackID, upstreamReq)
	if errDo != nil {
		return nil, errDo
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("commandcode upstream status %d: %s", resp.StatusCode, string(resp.Body))
	}
	body, errConvert := commandCodeNDJSONToChatCompletion(resp.Body, req.Model, time.Now().Unix())
	if errConvert != nil {
		return nil, errConvert
	}
	return okEnvelope(pluginapi.ExecutorResponse{
		Payload: body,
		Headers: http.Header{"Content-Type": []string{"application/json"}},
	})
}

func handleExecutorExecuteStream(raw []byte) ([]byte, error) {
	var req rpcExecutorRequest
	if errUnmarshal := json.Unmarshal(raw, &req); errUnmarshal != nil {
		return nil, errUnmarshal
	}
	upstreamReq, errBuild := buildCommandCodeHTTPRequest(req.ExecutorRequest, commandCodeConfig{}, "", time.Now().UTC().Format(time.DateOnly))
	if errBuild != nil {
		return nil, errBuild
	}
	resp, errDo := hostHTTPDoStream(req.HostCallbackID, upstreamReq)
	if errDo != nil {
		return nil, errDo
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("commandcode upstream status %d", resp.StatusCode)
	}
	go streamCommandCodeNDJSONToHost(req.StreamID, resp.StreamID, req.Model, time.Now().Unix())
	return okEnvelope(rpcExecutorStreamResponse{
		Headers: http.Header{"Content-Type": []string{"text/event-stream"}},
	})
}

func handleExecutorHTTPRequest(raw []byte) ([]byte, error) {
	var req struct {
		pluginapi.ExecutorHTTPRequest
		HostCallbackID string `json:"host_callback_id,omitempty"`
	}
	if errUnmarshal := json.Unmarshal(raw, &req); errUnmarshal != nil {
		return nil, errUnmarshal
	}
	authReq := pluginapi.HTTPRequest{
		Method:  req.Method,
		URL:     req.URL,
		Headers: req.Headers,
		Body:    req.Body,
	}
	resp, errDo := hostHTTPDo(req.HostCallbackID, authReq)
	if errDo != nil {
		return nil, errDo
	}
	return okEnvelope(pluginapi.ExecutorHTTPResponse{StatusCode: resp.StatusCode, Headers: resp.Headers, Body: resp.Body})
}

func commandCodeAuthDataFromRaw(raw []byte) (pluginapi.AuthData, error) {
	storage, errStorage := commandCodeAuthFromStorage(raw)
	if errStorage != nil {
		return pluginapi.AuthData{}, errStorage
	}
	return commandCodeAuthData(storage)
}

func streamCommandCodeNDJSONToHost(pluginStreamID, upstreamStreamID, model string, created int64) {
	defer func() {
		_ = callHost(pluginabi.MethodHostHTTPStreamClose, rpcHostHTTPStreamCloseRequest{StreamID: upstreamStreamID}, nil)
		_ = callHost(pluginabi.MethodHostStreamClose, rpcStreamCloseRequest{StreamID: pluginStreamID}, nil)
	}()
	var buffer []byte
	for {
		var readResp rpcHostHTTPStreamReadResponse
		if errRead := callHost(pluginabi.MethodHostHTTPStreamRead, rpcHostHTTPStreamReadRequest{StreamID: upstreamStreamID}, &readResp); errRead != nil {
			_ = callHost(pluginabi.MethodHostStreamClose, rpcStreamCloseRequest{StreamID: pluginStreamID, Error: errRead.Error()}, nil)
			return
		}
		if readResp.Error != "" {
			_ = callHost(pluginabi.MethodHostStreamClose, rpcStreamCloseRequest{StreamID: pluginStreamID, Error: readResp.Error}, nil)
			return
		}
		buffer = append(buffer, readResp.Payload...)
		for {
			index := bytes.IndexByte(buffer, '\n')
			if index < 0 {
				break
			}
			line := bytes.TrimSpace(buffer[:index])
			buffer = buffer[index+1:]
			if len(line) == 0 {
				continue
			}
			frames, errFrame := commandCodeEventToOpenAIStreamChunks(line, model, created)
			if errFrame != nil {
				_ = callHost(pluginabi.MethodHostStreamClose, rpcStreamCloseRequest{StreamID: pluginStreamID, Error: errFrame.Error()}, nil)
				return
			}
			for _, frame := range frames {
				if errEmit := callHost(pluginabi.MethodHostStreamEmit, rpcStreamEmitRequest{StreamID: pluginStreamID, Payload: frame}, nil); errEmit != nil {
					return
				}
			}
		}
		if readResp.Done {
			line := bytes.TrimSpace(buffer)
			if len(line) > 0 {
				frames, errFrame := commandCodeEventToOpenAIStreamChunks(line, model, created)
				if errFrame != nil {
					_ = callHost(pluginabi.MethodHostStreamClose, rpcStreamCloseRequest{StreamID: pluginStreamID, Error: errFrame.Error()}, nil)
					return
				}
				for _, frame := range frames {
					if errEmit := callHost(pluginabi.MethodHostStreamEmit, rpcStreamEmitRequest{StreamID: pluginStreamID, Payload: frame}, nil); errEmit != nil {
						return
					}
				}
			}
			return
		}
	}
}

func hostHTTPDo(callbackID string, req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
	var resp pluginapi.HTTPResponse
	err := callHost(pluginabi.MethodHostHTTPDo, rpcHostHTTPRequest{
		HostCallbackID: callbackID,
		Method:         req.Method,
		URL:            req.URL,
		Headers:        req.Headers,
		Body:           req.Body,
	}, &resp)
	return resp, err
}

func hostHTTPDoStream(callbackID string, req pluginapi.HTTPRequest) (rpcHostHTTPStreamResponse, error) {
	var resp rpcHostHTTPStreamResponse
	err := callHost(pluginabi.MethodHostHTTPDoStream, rpcHostHTTPRequest{
		HostCallbackID: callbackID,
		Method:         req.Method,
		URL:            req.URL,
		Headers:        req.Headers,
		Body:           req.Body,
	}, &resp)
	return resp, err
}

func callHost(method string, payload any, out any) error {
	rawPayload, errMarshal := json.Marshal(payload)
	if errMarshal != nil {
		return errMarshal
	}
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))
	var response C.cliproxy_buffer
	var req *C.uint8_t
	if len(rawPayload) > 0 {
		req = (*C.uint8_t)(C.CBytes(rawPayload))
		defer C.free(unsafe.Pointer(req))
	}
	if C.call_host_api(cMethod, req, C.size_t(len(rawPayload)), &response) != 0 {
		return fmt.Errorf("host callback %s failed", method)
	}
	if response.ptr == nil || response.len == 0 {
		return nil
	}
	defer C.free_host_buffer(response.ptr, response.len)
	rawResp := C.GoBytes(response.ptr, C.int(response.len))
	var envelope pluginabi.Envelope
	if errUnmarshal := json.Unmarshal(rawResp, &envelope); errUnmarshal != nil {
		return errUnmarshal
	}
	if !envelope.OK {
		if envelope.Error != nil {
			return fmt.Errorf("%s", envelope.Error.Message)
		}
		return fmt.Errorf("host callback %s returned error", method)
	}
	if out == nil || len(envelope.Result) == 0 {
		return nil
	}
	return json.Unmarshal(envelope.Result, out)
}

func okEnvelope(v any) ([]byte, error) {
	raw, errMarshal := json.Marshal(v)
	if errMarshal != nil {
		return nil, errMarshal
	}
	return json.Marshal(envelope{OK: true, Result: raw})
}

func errorEnvelope(code, message string) []byte {
	raw, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message}})
	return raw
}

func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	ptr := C.CBytes(raw)
	if ptr == nil {
		return
	}
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}
