package qbit

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// AddTorrent uploads a .torrent file to qBit, paused + skip-checking so it
// never downloads. Used by the webhook round-trip probe: adding a torrent
// makes qBit fire its configured "run on torrent added" program, which is
// the real call back to resolvarr we want to verify.
func (c *Client) AddTorrent(ctx context.Context, torrent []byte, fileName, category string) error {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("torrents", fileName+".torrent")
	if err != nil {
		return fmt.Errorf("multipart: %w", err)
	}
	if _, err := fw.Write(torrent); err != nil {
		return fmt.Errorf("multipart write: %w", err)
	}
	// paused + stopped cover qBit version naming differences; skip_checking
	// avoids a hash-check pass on a torrent that will never have data.
	_ = mw.WriteField("paused", "true")
	_ = mw.WriteField("stopped", "true")
	_ = mw.WriteField("skip_checking", "true")
	if strings.TrimSpace(category) != "" {
		_ = mw.WriteField("category", category)
	}
	if err := mw.Close(); err != nil {
		return fmt.Errorf("multipart close: %w", err)
	}
	resp, err := c.Do(ctx, "POST", "/api/v2/torrents/add", &buf, func(h http.Header) {
		h.Set("Content-Type", mw.FormDataContentType())
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != 200 {
		return fmt.Errorf("add torrent HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	// qBit returns "Ok." on success, "Fails." when it rejected the payload.
	if strings.HasPrefix(strings.TrimSpace(string(body)), "Fails") {
		return fmt.Errorf("qBittorrent rejected the test torrent")
	}
	return nil
}

// DeleteTorrent removes a torrent (and optionally its files) by hash.
func (c *Client) DeleteTorrent(ctx context.Context, hash string, deleteFiles bool) error {
	form := url.Values{"hashes": {hash}, "deleteFiles": {strconv.FormatBool(deleteFiles)}}
	resp, err := c.Do(ctx, "POST", "/api/v2/torrents/delete", strings.NewReader(form.Encode()), func(h http.Header) {
		h.Set("Content-Type", "application/x-www-form-urlencoded")
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("delete torrent HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}
