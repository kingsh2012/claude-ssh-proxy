package winagent

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	releaseAPIURL    = "https://api.github.com/repos/kingsh2012/claude-ssh-proxy/releases/latest"
	windowsAssetName = "aiagent-ssh-client-windows-amd64.zip"
	maxReleaseJSON   = 1 << 20
	maxChecksumFile  = 4 << 10
	maxClientArchive = 100 << 20
	maxClientBinary  = 100 << 20
)

var releaseVersionPattern = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)$`)

type UpgradeResult struct {
	Updated bool
	Version string
	LogPath string
}

type releaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type releaseMetadata struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

type preparedUpgrade struct {
	version string
	staged  string
}

func Upgrade(ctx context.Context, currentVersion string) (UpgradeResult, error) {
	executable, err := os.Executable()
	if err != nil {
		return UpgradeResult{}, errors.New("无法定位当前客户端程序")
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return UpgradeResult{}, errors.New("无法解析当前客户端路径")
	}
	prepared, err := prepareUpgrade(ctx, http.DefaultClient, releaseAPIURL, currentVersion, executable, true)
	if err != nil {
		return UpgradeResult{}, err
	}
	if prepared.staged == "" {
		return UpgradeResult{Version: prepared.version}, nil
	}
	defer func() {
		if prepared.staged != "" {
			_ = os.Remove(prepared.staged)
		}
	}()
	if err := preserveEmbeddedCA(executable); err != nil {
		return UpgradeResult{}, err
	}
	logPath := filepath.Join(filepath.Dir(executable), "aiagent-ssh-client-upgrade.log")
	if err := scheduleExecutableReplacement(executable, prepared.staged, logPath, os.Getpid()); err != nil {
		return UpgradeResult{}, fmt.Errorf("无法启动后台替换程序：%w", err)
	}
	prepared.staged = ""
	return UpgradeResult{Updated: true, Version: prepared.version, LogPath: logPath}, nil
}

func prepareUpgrade(ctx context.Context, client *http.Client, apiURL, currentVersion, executable string, requireGitHub bool) (preparedUpgrade, error) {
	metadataBytes, err := download(ctx, client, apiURL, maxReleaseJSON)
	if err != nil {
		return preparedUpgrade{}, fmt.Errorf("读取最新版本失败：%w", err)
	}
	var metadata releaseMetadata
	if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
		return preparedUpgrade{}, errors.New("最新版本信息格式无效")
	}
	if !releaseVersionPattern.MatchString(metadata.TagName) {
		return preparedUpgrade{}, errors.New("最新版本号格式无效")
	}
	if !newerReleaseAvailable(currentVersion, metadata.TagName) {
		return preparedUpgrade{version: currentVersion}, nil
	}
	archiveURL, checksumURL := "", ""
	for _, asset := range metadata.Assets {
		switch asset.Name {
		case windowsAssetName:
			archiveURL = asset.URL
		case windowsAssetName + ".sha256":
			checksumURL = asset.URL
		}
	}
	if archiveURL == "" || checksumURL == "" {
		return preparedUpgrade{}, errors.New("最新版本缺少Windows客户端或SHA-256校验文件")
	}
	if requireGitHub {
		if !trustedReleaseAssetURL(archiveURL) || !trustedReleaseAssetURL(checksumURL) {
			return preparedUpgrade{}, errors.New("发布文件下载地址不可信")
		}
	}
	checksumBytes, err := download(ctx, client, checksumURL, maxChecksumFile)
	if err != nil {
		return preparedUpgrade{}, fmt.Errorf("下载SHA-256校验文件失败：%w", err)
	}
	expected, err := parseChecksum(checksumBytes)
	if err != nil {
		return preparedUpgrade{}, err
	}
	archive, err := download(ctx, client, archiveURL, maxClientArchive)
	if err != nil {
		return preparedUpgrade{}, fmt.Errorf("下载Windows客户端失败：%w", err)
	}
	actual := sha256.Sum256(archive)
	if !bytes.Equal(actual[:], expected) {
		return preparedUpgrade{}, errors.New("Windows客户端SHA-256校验失败")
	}
	staged, err := extractClient(archive, executable)
	if err != nil {
		return preparedUpgrade{}, err
	}
	return preparedUpgrade{version: metadata.TagName, staged: staged}, nil
}

func download(ctx context.Context, client *http.Client, address string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "aiagent-ssh-client-updater")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP状态码%d", resp.StatusCode)
	}
	if resp.ContentLength > limit {
		return nil, errors.New("下载内容超过大小限制")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("下载内容超过大小限制")
	}
	return data, nil
}

func parseChecksum(data []byte) ([]byte, error) {
	fields := strings.Fields(string(data))
	if len(fields) < 2 || fields[1] != windowsAssetName {
		return nil, errors.New("SHA-256校验文件格式无效")
	}
	decoded, err := hex.DecodeString(fields[0])
	if err != nil || len(decoded) != sha256.Size {
		return nil, errors.New("SHA-256校验值无效")
	}
	return decoded, nil
}

func extractClient(archive []byte, executable string) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return "", errors.New("Windows客户端压缩包无效")
	}
	var binary *zip.File
	for _, entry := range reader.File {
		if !entry.FileInfo().IsDir() && strings.EqualFold(filepath.Base(filepath.FromSlash(entry.Name)), "aiagent-ssh-client.exe") {
			if binary != nil {
				return "", errors.New("Windows客户端压缩包包含多个客户端程序")
			}
			binary = entry
		}
	}
	if binary == nil || binary.UncompressedSize64 > maxClientBinary {
		return "", errors.New("Windows客户端压缩包中缺少有效程序")
	}
	source, err := binary.Open()
	if err != nil {
		return "", errors.New("无法读取Windows客户端程序")
	}
	defer source.Close()
	target, err := os.CreateTemp(filepath.Dir(executable), ".aiagent-ssh-client-update-*.exe")
	if err != nil {
		return "", fmt.Errorf("无法在客户端目录暂存更新：%w", err)
	}
	staged := target.Name()
	remove := true
	defer func() {
		target.Close()
		if remove {
			_ = os.Remove(staged)
		}
	}()
	written, err := io.Copy(target, io.LimitReader(source, maxClientBinary+1))
	if err != nil || written > maxClientBinary {
		return "", errors.New("无法解压Windows客户端程序")
	}
	if err := target.Close(); err != nil {
		return "", errors.New("无法写入Windows客户端程序")
	}
	remove = false
	return staged, nil
}

func trustedReleaseAssetURL(address string) bool {
	u, err := url.Parse(address)
	return err == nil && u.Scheme == "https" && strings.EqualFold(u.Host, "github.com") &&
		strings.HasPrefix(u.EscapedPath(), "/kingsh2012/claude-ssh-proxy/releases/download/")
}

func newerReleaseAvailable(current, latest string) bool {
	currentParts, currentOK := parseReleaseVersion(current)
	latestParts, latestOK := parseReleaseVersion(latest)
	if !latestOK || !currentOK {
		return current != latest
	}
	for i := range currentParts {
		if latestParts[i] != currentParts[i] {
			return latestParts[i] > currentParts[i]
		}
	}
	return false
}

func parseReleaseVersion(value string) ([3]int, bool) {
	var result [3]int
	match := releaseVersionPattern.FindStringSubmatch(value)
	if match == nil {
		return result, false
	}
	for i := range result {
		part, err := strconv.Atoi(match[i+1])
		if err != nil {
			return result, false
		}
		result[i] = part
	}
	return result, true
}
