// Package httpdissect implements a streaming HTTP/1.0 + HTTP/1.1 dissector
// that reassembles request/response pairs from raw TCP stream bytes.
//
// The dissector is stateful — it tracks per-connection (srcIP:srcPort →
// dstIP:dstPort) request queues so responses can be matched to their
// corresponding requests in pipelined or keep-alive connections.
//
// Limitations (acceptable for v1):
//   - HTTP/2 (gRPC) not supported — use the eBPF TLS tracer sidecar which
//     decodes HTTP/2 from decrypted bytes. Will be added in v2.
//   - WebSocket upgrade is detected but not decoded.
//   - Chunked transfer-encoding is decoded.
package httpdissect

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/textproto"
	"strconv"
	"strings"
)

// Message is a dissected HTTP request or response.
type Message struct {
	IsRequest bool
	Method    string
	Path      string
	Status    int
	Headers   map[string]string
	Body      []byte
}

// Dissector reassembles HTTP/1 messages from one direction of a TCP stream.
// Create one Dissector per direction per connection. Use a Pairer to match
// requests from the client-side Dissector with responses from the server-side.
const maxHTTPBufferBytes = 1 << 20

type Dissector struct {
	buf       bytes.Buffer
	inHeaders bool
}

// New creates a Dissector.
func New() *Dissector {
	return &Dissector{inHeaders: true}
}

// Feed appends new bytes to the dissector's internal buffer and returns any
// complete messages parsed. Calls to Feed must be in stream order.
func (d *Dissector) Feed(data []byte) ([]*Message, error) {
	if len(data) > maxHTTPBufferBytes-d.buf.Len() {
		d.buf.Reset()
		return nil, fmt.Errorf("http: stream buffer limit (%d bytes) exceeded; message discarded", maxHTTPBufferBytes)
	}
	d.buf.Write(data)
	var out []*Message
	for {
		msg, err := d.parseOne()
		if err == errIncomplete {
			break
		}
		if err != nil {
			return out, err
		}
		out = append(out, msg)
	}
	return out, nil
}

var errIncomplete = fmt.Errorf("incomplete message")

// parseOne attempts to parse a single HTTP message from the buffer.
func (d *Dissector) parseOne() (*Message, error) {
	if d.buf.Len() == 0 {
		return nil, errIncomplete
	}

	// find end of headers (\r\n\r\n)
	headerEnd := bytes.Index(d.buf.Bytes(), []byte("\r\n\r\n"))
	if headerEnd == -1 {
		return nil, errIncomplete
	}

	headerBytes := d.buf.Bytes()[:headerEnd]
	reader := bufio.NewReader(bytes.NewReader(headerBytes))
	tp := textproto.NewReader(reader)

	firstLine, err := tp.ReadLine()
	if err != nil {
		return nil, fmt.Errorf("http: read first line: %w", err)
	}

	mimeHeaders, err := tp.ReadMIMEHeader()
	if err != nil {
		return nil, fmt.Errorf("http: read headers: %w", err)
	}
	headers := make(map[string]string, len(mimeHeaders))
	for k, vs := range mimeHeaders {
		headers[strings.ToLower(k)] = strings.Join(vs, ", ")
	}

	msg := &Message{Headers: headers}
	bodyStart := headerEnd + 4

	// determine if request or response
	if strings.HasPrefix(firstLine, "HTTP/") {
		// response: "HTTP/1.1 200 OK"
		parts := strings.SplitN(firstLine, " ", 3)
		if len(parts) < 2 {
			return nil, fmt.Errorf("http: malformed response line: %s", firstLine)
		}
		msg.IsRequest = false
		status, _ := strconv.Atoi(parts[1])
		msg.Status = status
	} else {
		// request: "GET /path HTTP/1.1"
		parts := strings.SplitN(firstLine, " ", 3)
		if len(parts) < 3 {
			return nil, fmt.Errorf("http: malformed request line: %s", firstLine)
		}
		msg.IsRequest = true
		msg.Method = parts[0]
		msg.Path = parts[1]
	}

	// determine body length
	bodyLen, chunked, err := bodyLength(msg.Headers)
	if err != nil {
		return nil, err
	}

	var body []byte
	consumed := bodyStart
	if chunked {
		// parse chunked body
		body, consumed, err = d.parseChunkedBody(bodyStart)
		if err == errIncomplete {
			return nil, errIncomplete
		}
		if err != nil {
			return nil, err
		}
	} else if bodyLen > 0 {
		if d.buf.Len() < bodyStart+bodyLen {
			return nil, errIncomplete
		}
		body = make([]byte, bodyLen)
		copy(body, d.buf.Bytes()[bodyStart:bodyStart+bodyLen])
		consumed = bodyStart + bodyLen
	} else if bodyLen == -1 {
		// read until connection close — consume everything
		body = make([]byte, d.buf.Len()-bodyStart)
		copy(body, d.buf.Bytes()[bodyStart:])
		consumed = d.buf.Len()
	}
	msg.Body = body

	// consume the parsed bytes from the buffer
	d.buf.Next(consumed)
	return msg, nil
}

// bodyLength returns (length, isChunked, error).
// length == 0 means no body (e.g., GET request, or 204/304 response).
// length == -1 means read-until-close (HTTP/1.0 style).
func bodyLength(headers map[string]string) (int, bool, error) {
	if te := headers["transfer-encoding"]; strings.Contains(strings.ToLower(te), "chunked") {
		return 0, true, nil
	}
	if cl, ok := headers["content-length"]; ok {
		n, err := strconv.Atoi(cl)
		if err != nil {
			return 0, false, fmt.Errorf("http: bad content-length %q: %w", cl, err)
		}
		return n, false, nil
	}
	// no content-length, no chunked — assume no body for requests, read-until-close for responses
	return -1, false, nil
}

// parseChunkedBody parses an HTTP/1.1 chunked transfer-encoded body.
// Returns (body, totalBytesConsumed, error).
func (d *Dissector) parseChunkedBody(start int) ([]byte, int, error) {
	var body bytes.Buffer
	pos := start
	buf := d.buf.Bytes()

	for {
		// find end of chunk-size line
		lineEnd := bytes.Index(buf[pos:], []byte("\r\n"))
		if lineEnd == -1 {
			return nil, 0, errIncomplete
		}
		sizeStr := strings.TrimSpace(string(buf[pos : pos+lineEnd]))
		// chunk size may have extensions: "1a;ext=value"
		if semi := strings.Index(sizeStr, ";"); semi != -1 {
			sizeStr = sizeStr[:semi]
		}
		size, err := strconv.ParseInt(sizeStr, 16, 64)
		if err != nil {
			return nil, 0, fmt.Errorf("http: bad chunk size %q: %w", sizeStr, err)
		}
		pos += lineEnd + 2 // skip size line + \r\n

		if size == 0 {
			// last chunk — read trailing \r\n (or trailers + \r\n)
			// for simplicity, expect just \r\n
			if pos+2 > len(buf) {
				return nil, 0, errIncomplete
			}
			pos += 2
			return body.Bytes(), pos, nil
		}

		if pos+int(size)+2 > len(buf) {
			return nil, 0, errIncomplete
		}
		body.Write(buf[pos : pos+int(size)])
		pos += int(size) + 2 // skip chunk + \r\n
	}
}

// Pairer matches responses to requests in FIFO order.
// HTTP/1.1 pipelining guarantees responses arrive in request order on the
// same connection, so a simple queue suffices.
type Pairer struct {
	pending []*Message // outstanding requests
}

// NewPairer creates a Pairer.
func NewPairer() *Pairer {
	return &Pairer{}
}

// PushRequest queues a request awaiting a response.
func (p *Pairer) PushRequest(req *Message) {
	p.pending = append(p.pending, req)
}

// MatchResponse pops the oldest pending request and returns a Pair.
// Returns nil if no pending request exists (out-of-order response).
func (p *Pairer) MatchResponse(resp *Message) *Pair {
	if len(p.pending) == 0 {
		return nil
	}
	req := p.pending[0]
	p.pending = p.pending[1:]
	return &Pair{Request: req, Response: resp}
}

// Pair is a matched request/response.
type Pair struct {
	Request  *Message
	Response *Message
}

// Pending returns the count of unmatched requests.
func (p *Pairer) Pending() int {
	return len(p.pending)
}

// readAll is a helper for tests — feeds all data and returns all messages.
func readAll(data []byte) ([]*Message, error) {
	d := New()
	return d.Feed(data)
}

// suppress unused import warning
var _ = io.EOF
