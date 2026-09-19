package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
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
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/buildinfo"
)

const (
	defaultReleaseAPI        = "https://api.github.com/repos/Serialeo/agentdock/releases/latest"
	maxReleaseArchiveBytes   = 256 << 20
	maxDesktopArchiveBytes   = 512 << 20
	maxExtractedPayloadBytes = 64 << 20
	macOSDesktopArchiveName  = "AgentDock-macos-arm64.zip"
	coreSkillBundlePrefix    = "share/agentdock/core-skills/"
)

type release struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type CheckResult struct {
	CurrentVersion         string `json:"current_version"`
	LatestVersion          string `json:"latest_version"`
	DesktopCurrentVersion  string `json:"desktop_current_version,omitempty"`
	UpdateAvailable        bool   `json:"update_available"`
	DesktopUpdateAvailable bool   `json:"desktop_update_available,omitempty"`
	Message                string `json:"message"`
}

type updateInspection struct {
	Result               CheckResult
	ArchiveName          string
	ExecutableName       string
	ArchiveAsset         releaseAsset
	ChecksumAsset        releaseAsset
	DesktopArchiveAsset  releaseAsset
	DesktopChecksumAsset releaseAsset
}

type options struct {
	CurrentVersion        string
	ExecutablePath        string
	DesktopTargetPath     string
	DesktopCurrentVersion string
	DesktopOnly           bool
	GOOS                  string
	GOARCH                string
	ReleaseAPI            string
	HTTPClient            *http.Client
	Output                io.Writer
	Apply                 func(context.Context, applyRequest) (applyResult, error)
	VerifyBinary          func(context.Context, string, string) error
	ExtractDesktop        func(context.Context, []byte, string, string) (string, error)
}

type applyRequest struct {
	CurrentPath       string
	CurrentVersion    string
	StagedPath        string
	BundlePath        string
	DesktopTargetPath string
	DesktopStagedPath string
	DesktopOnly       bool
	TargetVersion     string
	Output            io.Writer
}

type applyResult struct {
	Restarted bool
	HandedOff bool
}

func Run(ctx context.Context, output io.Writer) error {
	opts, err := runtimeOptions(output)
	if err != nil {
		return err
	}
	return run(ctx, opts)
}

func Check(ctx context.Context) (CheckResult, error) {
	opts, err := runtimeOptions(io.Discard)
	if err != nil {
		return CheckResult{}, err
	}
	inspection, err := inspectUpdate(ctx, opts)
	if err != nil {
		return CheckResult{}, err
	}
	return inspection.Result, nil
}

func runtimeOptions(output io.Writer) (options, error) {
	executable, err := os.Executable()
	if err != nil {
		return options{}, fmt.Errorf("定位当前 AgentDock 二进制失败: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
		executable = resolved
	}
	desktopTarget := detectDesktopUpdateTarget()
	return options{
		CurrentVersion:        buildinfo.Version,
		ExecutablePath:        executable,
		DesktopTargetPath:     desktopTarget,
		DesktopCurrentVersion: desktopUpdateVersion(desktopTarget),
		DesktopOnly:           desktopUpdateOwnsExecutable(desktopTarget, executable),
		GOOS:                  runtime.GOOS,
		GOARCH:                runtime.GOARCH,
		ReleaseAPI:            defaultReleaseAPI,
		HTTPClient:            &http.Client{Timeout: 5 * time.Minute},
		Output:                output,
		Apply:                 applyPlatformUpdate,
		VerifyBinary:          verifyBinaryVersion,
		ExtractDesktop:        extractDesktopUpdateArchive,
	}, nil
}

func run(ctx context.Context, opts options) error {
	if opts.HTTPClient == nil {
		return errors.New("更新 HTTP 客户端不能为空")
	}
	if opts.Output == nil {
		opts.Output = io.Discard
	}
	if opts.Apply == nil || opts.VerifyBinary == nil {
		return errors.New("更新执行器未配置")
	}
	if opts.DesktopTargetPath != "" && opts.ExtractDesktop == nil {
		return errors.New("桌面更新解压器未配置")
	}

	inspection, err := inspectUpdate(ctx, opts)
	if err != nil {
		return err
	}
	if opts.DesktopOnly {
		return runDesktopOnlyUpdate(ctx, opts, inspection)
	}

	currentVersion := inspection.Result.CurrentVersion
	targetVersion := inspection.Result.LatestVersion
	archiveName := inspection.ArchiveName
	executableName := inspection.ExecutableName
	archiveAsset := inspection.ArchiveAsset
	checksumAsset := inspection.ChecksumAsset

	fmt.Fprintf(opts.Output, "当前版本：%s\n最新版本：%s\n\n", currentVersion, targetVersion)
	fmt.Fprintf(opts.Output, "正在下载 %s...\n", archiveName)
	archiveData, err := download(ctx, opts.HTTPClient, archiveAsset.URL, maxReleaseArchiveBytes)
	if err != nil {
		return fmt.Errorf("下载更新文件失败: %w", err)
	}
	checksumData, err := download(ctx, opts.HTTPClient, checksumAsset.URL, 1<<20)
	if err != nil {
		return fmt.Errorf("下载校验文件失败: %w", err)
	}
	if err := verifyChecksum(archiveData, checksumData); err != nil {
		return fmt.Errorf("更新文件校验失败，当前版本未被修改: %w", err)
	}
	fmt.Fprintln(opts.Output, "文件校验通过")

	tempDir, err := os.MkdirTemp("", "agentdock-update-*")
	if err != nil {
		return fmt.Errorf("创建更新临时目录失败: %w", err)
	}
	defer os.RemoveAll(tempDir)

	binaryData, err := extractExecutable(archiveData, opts.GOOS, executableName)
	if err != nil {
		return fmt.Errorf("解压更新文件失败: %w", err)
	}
	stagedPath := filepath.Join(tempDir, executableName)
	if err := os.WriteFile(stagedPath, binaryData, 0o755); err != nil {
		return fmt.Errorf("写入新版本二进制失败: %w", err)
	}
	bundlePath, err := extractCoreSkillBundle(archiveData, opts.GOOS, tempDir)
	if err != nil {
		return fmt.Errorf("解压核心 Skill Bundle 失败: %w", err)
	}
	if err := opts.VerifyBinary(ctx, stagedPath, targetVersion); err != nil {
		return fmt.Errorf("新版本二进制验证失败，当前版本未被修改: %w", err)
	}

	desktopStagedPath := ""
	if inspection.DesktopArchiveAsset.Name != "" {
		desktopArchiveData := archiveData
		if inspection.DesktopArchiveAsset.Name != archiveAsset.Name || inspection.DesktopArchiveAsset.URL != archiveAsset.URL {
			fmt.Fprintf(opts.Output, "正在下载 %s...\n", inspection.DesktopArchiveAsset.Name)
			var downloadErr error
			desktopArchiveData, downloadErr = download(ctx, opts.HTTPClient, inspection.DesktopArchiveAsset.URL, maxDesktopArchiveBytes)
			if downloadErr != nil {
				return fmt.Errorf("下载桌面组件更新文件失败: %w", downloadErr)
			}
			desktopChecksumData, checksumErr := download(ctx, opts.HTTPClient, inspection.DesktopChecksumAsset.URL, 1<<20)
			if checksumErr != nil {
				return fmt.Errorf("下载桌面组件校验文件失败: %w", checksumErr)
			}
			if checksumErr := verifyChecksum(desktopArchiveData, desktopChecksumData); checksumErr != nil {
				return fmt.Errorf("桌面组件更新文件校验失败，当前版本未被修改: %w", checksumErr)
			}
		}
		desktopStagedPath, err = opts.ExtractDesktop(ctx, desktopArchiveData, tempDir, targetVersion)
		if err != nil {
			return fmt.Errorf("解压桌面组件更新文件失败: %w", err)
		}
		fmt.Fprintln(opts.Output, "桌面组件更新文件校验通过")
	}

	fmt.Fprintln(opts.Output, "正在备份并安装新版本...")
	result, err := opts.Apply(ctx, applyRequest{
		CurrentPath:       opts.ExecutablePath,
		CurrentVersion:    currentVersion,
		StagedPath:        stagedPath,
		BundlePath:        bundlePath,
		DesktopTargetPath: opts.DesktopTargetPath,
		DesktopStagedPath: desktopStagedPath,
		TargetVersion:     targetVersion,
		Output:            opts.Output,
	})
	if err != nil {
		return err
	}
	if result.HandedOff {
		fmt.Fprintf(opts.Output, "Windows 更新已交给辅助进程，将自动替换并重启到 %s。\n", targetVersion)
		return nil
	}
	if result.Restarted {
		fmt.Fprintf(opts.Output, "更新完成并已重启：%s → %s\n", currentVersion, targetVersion)
	} else {
		fmt.Fprintf(opts.Output, "更新完成：%s → %s。当前未检测到托管服务，请重新启动正在运行的 AgentDock。\n", currentVersion, targetVersion)
	}
	return nil
}

func runDesktopOnlyUpdate(ctx context.Context, opts options, inspection updateInspection) error {
	targetVersion := inspection.Result.LatestVersion
	asset := inspection.DesktopArchiveAsset
	checksumAsset := inspection.DesktopChecksumAsset
	if asset.Name == "" || checksumAsset.Name == "" {
		return errors.New("桌面组件更新缺少 Release 资源")
	}

	fmt.Fprintf(opts.Output, "当前版本：%s\n最新版本：%s\n\n", inspection.Result.CurrentVersion, targetVersion)
	fmt.Fprintf(opts.Output, "正在下载 %s...\n", asset.Name)
	archiveData, err := download(ctx, opts.HTTPClient, asset.URL, maxDesktopArchiveBytes)
	if err != nil {
		return fmt.Errorf("下载桌面组件更新文件失败: %w", err)
	}
	checksumData, err := download(ctx, opts.HTTPClient, checksumAsset.URL, 1<<20)
	if err != nil {
		return fmt.Errorf("下载桌面组件校验文件失败: %w", err)
	}
	if err := verifyChecksum(archiveData, checksumData); err != nil {
		return fmt.Errorf("桌面组件更新文件校验失败，当前版本未被修改: %w", err)
	}

	tempDir, err := os.MkdirTemp("", "agentdock-desktop-update-*")
	if err != nil {
		return fmt.Errorf("创建桌面组件更新临时目录失败: %w", err)
	}
	defer os.RemoveAll(tempDir)
	stagedDesktop, err := opts.ExtractDesktop(ctx, archiveData, tempDir, targetVersion)
	if err != nil {
		return fmt.Errorf("解压桌面组件更新文件失败: %w", err)
	}
	fmt.Fprintln(opts.Output, "桌面组件校验通过")

	_, err = opts.Apply(ctx, applyRequest{
		CurrentPath:       opts.ExecutablePath,
		CurrentVersion:    inspection.Result.CurrentVersion,
		DesktopTargetPath: opts.DesktopTargetPath,
		DesktopStagedPath: stagedDesktop,
		DesktopOnly:       true,
		TargetVersion:     targetVersion,
		Output:            opts.Output,
	})
	return err
}

func inspectUpdate(ctx context.Context, opts options) (updateInspection, error) {
	if opts.HTTPClient == nil {
		return updateInspection{}, errors.New("更新 HTTP 客户端不能为空")
	}
	latest, err := fetchLatestRelease(ctx, opts.HTTPClient, opts.ReleaseAPI)
	if err != nil {
		return updateInspection{}, err
	}

	currentVersion := normalizeVersion(opts.CurrentVersion)
	targetVersion := normalizeVersion(latest.TagName)
	desktopVersion := normalizeVersion(opts.DesktopCurrentVersion)
	updateDesktop := strings.TrimSpace(opts.DesktopTargetPath) != ""

	// 同一版本号可以重新发布；每次更新都安装远端 latest 的完整载荷，不能按本地版本号跳过。
	result := CheckResult{
		CurrentVersion:         currentVersion,
		LatestVersion:          targetVersion,
		DesktopCurrentVersion:  desktopVersion,
		UpdateAvailable:        true,
		DesktopUpdateAvailable: updateDesktop,
		Message:                fmt.Sprintf("将从远端下载并安装最新发布：%s → %s", currentVersion, targetVersion),
	}

	if opts.DesktopOnly {
		desktopArchiveAsset, ok := findAsset(latest.Assets, macOSDesktopArchiveName)
		if !ok {
			return updateInspection{}, fmt.Errorf("Release %s 缺少 macOS 桌面更新文件 %s", targetVersion, macOSDesktopArchiveName)
		}
		desktopChecksumAsset, ok := findAsset(latest.Assets, macOSDesktopArchiveName+".sha256")
		if !ok {
			return updateInspection{}, fmt.Errorf("Release %s 缺少校验文件 %s.sha256", targetVersion, macOSDesktopArchiveName)
		}
		result.DesktopUpdateAvailable = true
		return updateInspection{
			Result:               result,
			DesktopArchiveAsset:  desktopArchiveAsset,
			DesktopChecksumAsset: desktopChecksumAsset,
		}, nil
	}

	archiveName, executableName, err := platformAssetNames(opts.GOOS, opts.GOARCH)
	if err != nil {
		return updateInspection{}, err
	}
	archiveAsset, ok := findAsset(latest.Assets, archiveName)
	if !ok {
		return updateInspection{}, fmt.Errorf("Release %s 缺少当前平台文件 %s", targetVersion, archiveName)
	}
	checksumAsset, ok := findAsset(latest.Assets, archiveName+".sha256")
	if !ok {
		return updateInspection{}, fmt.Errorf("Release %s 缺少校验文件 %s.sha256", targetVersion, archiveName)
	}

	desktopArchiveAsset := releaseAsset{}
	desktopChecksumAsset := releaseAsset{}
	if updateDesktop {
		switch opts.GOOS {
		case "darwin":
			desktopArchiveAsset, ok = findAsset(latest.Assets, macOSDesktopArchiveName)
			if !ok {
				return updateInspection{}, fmt.Errorf("Release %s 缺少 macOS 桌面更新文件 %s", targetVersion, macOSDesktopArchiveName)
			}
			desktopChecksumAsset, ok = findAsset(latest.Assets, macOSDesktopArchiveName+".sha256")
			if !ok {
				return updateInspection{}, fmt.Errorf("Release %s 缺少校验文件 %s.sha256", targetVersion, macOSDesktopArchiveName)
			}
		case "windows":
			// Windows 控制面板与核心位于同一个 Release ZIP；复用已经校验过的归档，
			// 避免同一次升级重复下载数百 MB 文件。
			desktopArchiveAsset = archiveAsset
			desktopChecksumAsset = checksumAsset
		}
	}

	return updateInspection{
		Result:               result,
		ArchiveName:          archiveName,
		ExecutableName:       executableName,
		ArchiveAsset:         archiveAsset,
		ChecksumAsset:        checksumAsset,
		DesktopArchiveAsset:  desktopArchiveAsset,
		DesktopChecksumAsset: desktopChecksumAsset,
	}, nil
}

func fetchLatestRelease(ctx context.Context, client *http.Client, endpoint string) (release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return release{}, fmt.Errorf("创建 Release 请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "agentdock/"+buildinfo.Version)
	resp, err := client.Do(req)
	if err != nil {
		return release{}, fmt.Errorf("查询最新 Release 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return release{}, fmt.Errorf("查询最新 Release 失败: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var latest release
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 2<<20))
	if err := decoder.Decode(&latest); err != nil {
		return release{}, fmt.Errorf("解析最新 Release 失败: %w", err)
	}
	if normalizeVersion(latest.TagName) == "" {
		return release{}, errors.New("最新 Release 缺少 tag_name")
	}
	return latest, nil
}

func download(ctx context.Context, client *http.Client, rawURL string, limit int64) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, fmt.Errorf("无效下载地址 %q", rawURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "agentdock/"+buildinfo.Version)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	reader := io.LimitReader(resp.Body, limit+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("下载内容超过 %d 字节限制", limit)
	}
	return data, nil
}

func verifyChecksum(data, checksumFile []byte) error {
	fields := strings.Fields(string(checksumFile))
	if len(fields) == 0 {
		return errors.New("校验文件为空")
	}
	expected := strings.ToLower(strings.TrimSpace(fields[0]))
	if len(expected) != sha256.Size*2 {
		return errors.New("SHA-256 格式无效")
	}
	if _, err := hex.DecodeString(expected); err != nil {
		return errors.New("SHA-256 格式无效")
	}
	actual := sha256.Sum256(data)
	if hex.EncodeToString(actual[:]) != expected {
		return errors.New("SHA-256 不匹配")
	}
	return nil
}

func extractExecutable(archiveData []byte, goos, executableName string) ([]byte, error) {
	if goos == "windows" {
		reader, err := zip.NewReader(bytes.NewReader(archiveData), int64(len(archiveData)))
		if err != nil {
			return nil, err
		}
		for _, file := range reader.File {
			if filepath.ToSlash(file.Name) != executableName {
				continue
			}
			opened, err := file.Open()
			if err != nil {
				return nil, err
			}
			defer opened.Close()
			return readLimited(opened, maxExtractedPayloadBytes)
		}
		return nil, fmt.Errorf("压缩包缺少 %s", executableName)
	}

	gzipReader, err := gzip.NewReader(bytes.NewReader(archiveData))
	if err != nil {
		return nil, err
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	wanted := "bin/" + executableName
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		name := strings.TrimPrefix(filepath.ToSlash(header.Name), "./")
		if name != wanted || !header.FileInfo().Mode().IsRegular() {
			continue
		}
		return readLimited(tarReader, maxExtractedPayloadBytes)
	}
	return nil, fmt.Errorf("压缩包缺少 %s", wanted)
}

func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("解压后的二进制超过 %d 字节限制", limit)
	}
	return data, nil
}

func verifyBinaryVersion(ctx context.Context, binaryPath, targetVersion string) error {
	verifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(verifyCtx, binaryPath, "--version")
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("执行 --version 失败: %w: %s", err, strings.TrimSpace(string(output)))
	}
	actualVersion, err := parseVersionOutput(output)
	if err != nil {
		return err
	}
	if actualVersion != normalizeVersion(targetVersion) {
		return fmt.Errorf("版本输出为 %s，目标版本为 %s", actualVersion, normalizeVersion(targetVersion))
	}
	return nil
}

func parseVersionOutput(output []byte) (string, error) {
	firstLine := strings.TrimSpace(strings.SplitN(string(output), "\n", 2)[0])
	const prefix = "AgentDock v"
	if !strings.HasPrefix(firstLine, prefix) {
		return "", fmt.Errorf("无法识别版本输出 %q", firstLine)
	}
	version := normalizeVersion(strings.TrimPrefix(firstLine, prefix))
	if _, valid := compareVersions(version, version); !valid {
		return "", fmt.Errorf("版本输出格式无效 %q", firstLine)
	}
	return version, nil
}

func platformAssetNames(goos, goarch string) (archiveName, executableName string, err error) {
	switch {
	case goos == "linux" && goarch == "amd64":
		return "agentdock_linux_amd64.tar.gz", "agentdock", nil
	case goos == "darwin" && goarch == "arm64":
		return "agentdock_darwin_arm64.tar.gz", "agentdock", nil
	case goos == "windows" && goarch == "amd64":
		return "agentdock_windows_amd64.zip", "agentdock.exe", nil
	default:
		return "", "", fmt.Errorf("当前平台不在支持范围：%s/%s（仅支持 linux/amd64、darwin/arm64、windows/amd64）", goos, goarch)
	}
}

func findAsset(assets []releaseAsset, name string) (releaseAsset, bool) {
	for _, asset := range assets {
		if asset.Name == name && strings.TrimSpace(asset.URL) != "" {
			return asset, true
		}
	}
	return releaseAsset{}, false
}

func normalizeVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" {
		return ""
	}
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	return version
}

func compareVersions(left, right string) (int, bool) {
	parse := func(value string) ([3]int, bool) {
		var parsed [3]int
		value = strings.TrimPrefix(normalizeVersion(value), "v")
		value = strings.SplitN(value, "-", 2)[0]
		parts := strings.Split(value, ".")
		if len(parts) != len(parsed) {
			return parsed, false
		}
		for index, part := range parts {
			number, err := strconv.Atoi(part)
			if err != nil || number < 0 {
				return parsed, false
			}
			parsed[index] = number
		}
		return parsed, true
	}
	leftVersion, leftOK := parse(left)
	rightVersion, rightOK := parse(right)
	if !leftOK || !rightOK {
		return 0, false
	}
	for index := range leftVersion {
		if leftVersion[index] < rightVersion[index] {
			return -1, true
		}
		if leftVersion[index] > rightVersion[index] {
			return 1, true
		}
	}
	return 0, true
}
