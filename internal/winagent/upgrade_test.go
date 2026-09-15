package winagent

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareUpgradeChecksHashAndExtractsClient(t *testing.T) {
	const binaryContent = "new-windows-client"
	var archive bytes.Buffer
	zipWriter := zip.NewWriter(&archive)
	entry, err := zipWriter.Create("aiagent-ssh-client-windows-amd64/aiagent-ssh-client.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte(binaryContent)); err != nil {
		t.Fatal(err)
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(archive.Bytes())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			_ = json.NewEncoder(w).Encode(releaseMetadata{
				TagName: "v1.2.3",
				Assets: []releaseAsset{
					{Name: windowsAssetName, URL: "http://" + r.Host + "/client.zip"},
					{Name: windowsAssetName + ".sha256", URL: "http://" + r.Host + "/client.zip.sha256"},
				},
			})
		case "/client.zip":
			_, _ = w.Write(archive.Bytes())
		case "/client.zip.sha256":
			_, _ = fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), windowsAssetName)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	executable := filepath.Join(t.TempDir(), "aiagent-ssh-client.exe")
	prepared, err := prepareUpgrade(context.Background(), server.Client(), server.URL+"/latest", "v1.2.2", executable, false)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(prepared.staged)
	if prepared.version != "v1.2.3" || prepared.staged == "" {
		t.Fatalf("升级准备结果错误：%+v", prepared)
	}
	data, err := os.ReadFile(prepared.staged)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != binaryContent {
		t.Fatalf("解压后的客户端内容错误：%q", data)
	}

	current, err := prepareUpgrade(context.Background(), server.Client(), server.URL+"/latest", "v1.2.3", executable, false)
	if err != nil {
		t.Fatal(err)
	}
	if current.staged != "" || current.version != "v1.2.3" {
		t.Fatalf("相同版本不应下载更新：%+v", current)
	}
}

func TestPrepareUpgradeRejectsWrongHash(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			_ = json.NewEncoder(w).Encode(releaseMetadata{
				TagName: "v1.2.3",
				Assets: []releaseAsset{
					{Name: windowsAssetName, URL: "http://" + r.Host + "/client.zip"},
					{Name: windowsAssetName + ".sha256", URL: "http://" + r.Host + "/client.zip.sha256"},
				},
			})
		case "/client.zip":
			_, _ = w.Write([]byte("not-the-published-file"))
		case "/client.zip.sha256":
			_, _ = fmt.Fprintf(w, "%064d  %s\n", 0, windowsAssetName)
		}
	}))
	defer server.Close()

	_, err := prepareUpgrade(context.Background(), server.Client(), server.URL+"/latest", "v1.2.2", filepath.Join(t.TempDir(), "aiagent-ssh-client.exe"), false)
	if err == nil || err.Error() != "Windows客户端SHA-256校验失败" {
		t.Fatalf("错误SHA-256未被拒绝：%v", err)
	}
}

func TestNewerReleaseAvailable(t *testing.T) {
	for _, test := range []struct {
		current string
		latest  string
		want    bool
	}{
		{"v1.2.2", "v1.2.3", true},
		{"v1.2.3", "v1.2.3", false},
		{"v1.3.0", "v1.2.3", false},
		{"dev", "v1.2.3", true},
	} {
		if got := newerReleaseAvailable(test.current, test.latest); got != test.want {
			t.Fatalf("newerReleaseAvailable(%q, %q)=%t，期望%t", test.current, test.latest, got, test.want)
		}
	}
}
