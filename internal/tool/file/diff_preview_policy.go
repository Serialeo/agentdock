package file

const defaultDiffPreviewBytes = 64 << 10

// diffPreviewOptions 将 diff 从“每次写入都生成”收敛为显式预览能力。
// dry_run 默认返回预览；真实写入只有显式提供 max_diff_bytes 时才返回，
// 避免大文件修改在已经提交副作用后再生成、复制和传输大块 diff。
func diffPreviewOptions(request EditRequest) (maxBytes int, enabled bool) {
	if request.MaxDiffBytes != nil {
		return boundedInt(*request.MaxDiffBytes, defaultDiffPreviewBytes, 1, maxTextOutputBytes), true
	}
	if request.DryRun {
		return defaultDiffPreviewBytes, true
	}
	return 0, false
}

func addDiffPreviewFields(result Result, preview string, truncated bool, stats diffStats) {
	result["diff_preview"] = preview
	result["truncated"] = truncated
	result["files_changed"] = stats.FilesChanged
	result["insertions"] = stats.Insertions
	result["deletions"] = stats.Deletions
}
