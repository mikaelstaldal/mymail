// Package ldaprotocol defines the wire contract shared by the LDA server and
// thin client, without pulling database dependencies into the client.
package ldaprotocol

// MaxMessageBytes is the maximum accepted raw RFC 5322 message size.
const MaxMessageBytes = 64 << 20 // 64 MiB
