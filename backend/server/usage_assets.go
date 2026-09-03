package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// 本文件实现「base64 媒体外置」：请求体（四段链路）中的图片/音频/视频/文件
// base64 数据在清洗期被提取为独立文件，body 内以占位符替代。两阶段设计：
//
//   - 捕获期（请求路径上）：assetSink.extractFromValue 只解码、按内容哈希去重、
//     登记到 record.assets，并把原字符串替换为占位符——零文件 IO；
//   - 落库期（writer goroutine）：persistUsageRecord 判定该记录的 body 会被
//     保留后，才把登记的资产写入 <DB目录>/usage-assets/<requestId>/。
//
// 这样 bodyOnErrorOnly 开启时，成功请求不产生任何磁盘写。

// AssetPlaceholderPrefix 是请求体内媒体占位符的前缀，完整格式：
// __ELYSIA_ASSET__:<requestId>/<hash>.<ext>
const AssetPlaceholderPrefix = "__ELYSIA_ASSET__:"

// mediaAssetMinBase64Len 是外置的最小 base64 长度（约 384B 解码后）。
// 低于该长度的 base64 串（小图标、短哈希等）视为普通字段原样保留，
// 避免误伤普通业务数据。
const mediaAssetMinBase64Len = 512

// mediaAsset 是一条待外置媒体。
type mediaAsset struct {
	Hash string // sha256(解码字节) 十六进制前 16 位，兼作文件名主干
	Ext  string // 由 mime/format 映射；未知 bin
	Mime string
	Data []byte
}

// assetSink 挂在 usageRecord 上收集外置媒体；按 Hash 去重，同一图片出现在
// incoming/outgoing/downstream 多段时只落一份文件、共用同一占位符。
type assetSink struct {
	requestID string
	items     []mediaAsset
	byHash    map[string]int
	// memo 缓存「原始字符串指纹 → 占位符」，流式场景同一媒体会随 events
	// 数组反复重清洗，避免每次重新解码+哈希。
	memo map[string]string
}

func newAssetSink(requestID string) assetSink {
	return assetSink{
		requestID: requestID,
		byHash:    make(map[string]int),
		memo:      make(map[string]string),
	}
}

// clone 返回深拷贝（解码字节一并复制），供 enqueueUsageRecord 断开与请求
// goroutine 的底层数组别名。
func (a *assetSink) clone() assetSink {
	cloned := assetSink{
		requestID: a.requestID,
		items:     make([]mediaAsset, len(a.items)),
		byHash:    make(map[string]int, len(a.byHash)),
		memo:      make(map[string]string, len(a.memo)),
	}
	for i, item := range a.items {
		data := make([]byte, len(item.Data))
		copy(data, item.Data)
		cloned.items[i] = mediaAsset{Hash: item.Hash, Ext: item.Ext, Mime: item.Mime, Data: data}
	}
	for k, v := range a.byHash {
		cloned.byHash[k] = v
	}
	for k, v := range a.memo {
		cloned.memo[k] = v
	}
	return cloned
}

// count 返回已登记（去重后）的媒体数。
func (a *assetSink) count() int {
	return len(a.items)
}

// extractFromValue 递归遍历已解析的 JSON 值，把命中的 base64 媒体字符串原地
// 替换为占位符并登记资产。识别规则见 shouldExtractKey/dataURI 帮助函数。
func (a *assetSink) extractFromValue(value interface{}) {
	switch v := value.(type) {
	case map[string]interface{}:
		for key, child := range v {
			if childStr, ok := child.(string); ok {
				if replaced, hit := a.extractString(key, v, childStr); hit {
					v[key] = replaced
					continue
				}
			}
			a.extractFromValue(child)
		}
	case []interface{}:
		for _, child := range v {
			a.extractFromValue(child)
		}
	}
}

// extractFromSSE 对 SSE 流做逐行 best-effort 外置：仅处理 data: 前缀且含媒体
// 标记的行，单行 JSON 解析失败则原样保留。返回处理后的完整文本。
func (a *assetSink) extractFromSSE(content string) string {
	if !strings.Contains(content, ";base64,") && !strings.Contains(content, "b64_json") {
		return content
	}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		if !strings.HasPrefix(trimmed, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		if !strings.Contains(payload, ";base64,") && !strings.Contains(payload, "b64_json") {
			continue
		}
		var value interface{}
		if err := json.Unmarshal([]byte(payload), &value); err != nil {
			continue
		}
		a.extractFromValue(value)
		if replaced, err := json.Marshal(value); err == nil {
			// 用定位而非 TrimSuffix 重建：payload 已经 TrimSpace，CRLF/尾空白行
			// 不以 payload 结尾，TrimSuffix 不匹配会把原文与替换体拼在同一行
			// （原文 base64 未外置 + 追加了第二份 JSON）。行尾的 \r 随替换丢弃，
			// 对存储的日志内容无害。
			if idx := strings.LastIndex(line, payload); idx >= 0 {
				lines[i] = line[:idx] + string(replaced)
			}
		}
	}
	return strings.Join(lines, "\n")
}

// extractString 判断 map 中 key 处的字符串值是否为可外置媒体；命中则返回占位符。
func (a *assetSink) extractString(key string, parent map[string]interface{}, raw string) (string, bool) {
	if len(raw) < mediaAssetMinBase64Len {
		return raw, false
	}
	// 情形一：data URI（data:<mime>;base64,<payload>）——任意键下的字符串
	// 都可能是媒体（OpenAI image_url.url、Responses input_image 等）。
	if mime, payload, ok := parseMediaDataURL(raw); ok {
		return a.registerMemo(raw, mime, "", payload), true
	}
	// 情形二：裸 base64 + 兄弟字段携带类型信息。
	switch normalizedKey(key) {
	case "data":
		// Claude source:{type:"base64",media_type}、OpenAI input_audio:{format}、
		// Gemini inlineData/inline_data:{mimeType|mime_type}——data 键配合
		// 兄弟类型字段（键名归一匹配，兼容驼峰/下划线两种线格式）。
		if mime, ok := siblingMediaHint(parent); ok {
			return a.registerMemo(raw, mime, "", raw), true
		}
	case "b64json":
		// OpenAI 图像响应无 mime 兄弟字段，按 png 处理（官方默认格式）。
		return a.registerMemo(raw, "image/png", "", raw), true
	}
	return raw, false
}

// registerMemo 带指纹缓存的登记：解码失败时返回原文（不外置）。
func (a *assetSink) registerMemo(raw, mime, ext, base64Payload string) string {
	fp := fingerprint(raw)
	if placeholder, ok := a.memo[fp]; ok {
		return placeholder
	}
	placeholder, ok := a.register(mime, ext, base64Payload)
	if !ok {
		return raw
	}
	a.memo[fp] = placeholder
	return placeholder
}

// register 解码、登记（按哈希去重）并返回占位符。解码失败返回 ok=false。
func (a *assetSink) register(mime, ext, base64Payload string) (string, bool) {
	data, err := base64.StdEncoding.DecodeString(base64Payload)
	if err != nil || len(data) == 0 {
		return "", false
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])[:16]
	if ext == "" {
		ext = mediaExtFromMime(mime)
	}
	if idx, ok := a.byHash[hash]; ok {
		item := a.items[idx]
		return assetPlaceholder(a.requestID, item.Hash, item.Ext), true
	}
	a.byHash[hash] = len(a.items)
	a.items = append(a.items, mediaAsset{Hash: hash, Ext: ext, Mime: mime, Data: data})
	return assetPlaceholder(a.requestID, hash, ext), true
}

func assetPlaceholder(requestID, hash, ext string) string {
	return AssetPlaceholderPrefix + requestID + "/" + hash + "." + ext
}

// parseMediaDataURL 解析 data:<mime>;base64,<payload>，mime 限定为媒体大类
// （image/audio/video/application）。
func parseMediaDataURL(raw string) (mime, payload string, ok bool) {
	if !strings.HasPrefix(raw, "data:") {
		return "", "", false
	}
	rest := raw[len("data:"):]
	semi := strings.Index(rest, ",")
	if semi < 0 {
		return "", "", false
	}
	meta := rest[:semi]
	if !strings.HasSuffix(meta, ";base64") {
		return "", "", false
	}
	mime = strings.TrimSuffix(meta, ";base64")
	if !isMediaMime(mime) {
		return "", "", false
	}
	payload = rest[semi+1:]
	if len(payload) < mediaAssetMinBase64Len {
		return "", "", false
	}
	return mime, payload, true
}

func isMediaMime(mime string) bool {
	return strings.HasPrefix(mime, "image/") ||
		strings.HasPrefix(mime, "audio/") ||
		strings.HasPrefix(mime, "video/") ||
		strings.HasPrefix(mime, "application/")
}

// mediaExtFromMime 由 mime 推断扩展名；未知类型一律 bin。
func mediaExtFromMime(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/png":
		return "png"
	case "image/jpeg", "image/jpg":
		return "jpg"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	case "audio/mpeg", "audio/mp3":
		return "mp3"
	case "audio/wav", "audio/x-wav":
		return "wav"
	case "audio/ogg":
		return "ogg"
	case "video/mp4":
		return "mp4"
	case "video/webm":
		return "webm"
	case "application/pdf":
		return "pdf"
	default:
		return "bin"
	}
}

func normalizedKey(key string) string {
	return strings.ToLower(strings.ReplaceAll(key, "_", ""))
}

// siblingMediaHint 在同级字段中寻找媒体类型提示：media_type/mime_type/
// mimeType → mime；format（OpenAI input_audio 的 wav/mp3）→ 映射为
// audio/<format>。键名归一（小写、去下划线）后匹配，兼容两种线格式。
func siblingMediaHint(parent map[string]interface{}) (string, bool) {
	for k, v := range parent {
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		switch normalizedKey(k) {
		case "mediatype", "mimetype":
			return s, true
		case "format":
			return "audio/" + s, true
		}
	}
	return "", false
}

// fingerprint 生成原始字符串的轻量指纹（长度 + 首尾片段），供 memo 命中。
// 长度必须参与：只比首尾两段时，同生成器的两张图（头部/尾部结构相同、
// 中段不同）会撞指纹，后者的占位符会指向前者——是内容替换而非良性复用。
// 加入长度后碰撞概率可忽略；仍限定同一请求内使用。
func fingerprint(s string) string {
	const seg = 48
	if len(s) <= seg*2 {
		return s
	}
	var b strings.Builder
	b.Grow(seg*2 + 12)
	b.WriteString(strconv.Itoa(len(s)))
	b.WriteByte('|')
	b.WriteString(s[:seg])
	b.WriteByte('|')
	b.WriteString(s[len(s)-seg:])
	return b.String()
}

// parseAssetFileName 校验资产文件名并拆分 (hash, ext)；管理端资产路由用。
func parseAssetFileName(name string) (hash, ext string, ok bool) {
	dot := strings.LastIndex(name, ".")
	if dot <= 0 || dot == len(name)-1 {
		return "", "", false
	}
	hash, ext = name[:dot], name[dot+1:]
	if len(hash) != 16 {
		return "", "", false
	}
	for _, c := range hash {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return "", "", false
		}
	}
	for _, c := range ext {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
			return "", "", false
		}
	}
	return hash, ext, true
}

// usageAssetsDirName 是数据库同目录下的资产根目录名。
const usageAssetsDirName = "usage-assets"

// writeUsageAssets 把登记的媒体写入 <DB目录>/usage-assets/（扁平内容寻址：
// 文件名即内容哈希）。同名文件已存在则跳过——同一图片无论出现在多少个
// 请求里（多轮对话每轮重发全部历史是常态）都只占一份磁盘；能否删除由
// usage_asset_refs 引用计数决定（见 DeleteUsageAssetRefs）。
func writeUsageAssets(root string, items []mediaAsset) (int, error) {
	if root == "" || len(items) == 0 {
		return 0, nil
	}
	written := 0
	if err := os.MkdirAll(root, 0o755); err != nil {
		return written, err
	}
	for _, item := range items {
		path := filepath.Join(root, item.Hash+"."+item.Ext)
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := os.WriteFile(path, item.Data, 0o644); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

// migrateUsageAssetsLayout 把旧的按请求分目录布局迁移到扁平内容寻址布局，
// 并从存留记录的 record_json 重建 usage_asset_refs 引用表。幂等：目录里
// 没有子目录即视为已迁移（每启动一次廉价检查）。迁移失败的文件留在原地
// 交给孤儿清扫宽限回收，绝不因迁移异常阻塞启动。
//
// 背景：旧布局 usage-assets/<requestId>/<hash>.<ext> 只在单请求内去重，
// 多轮对话每轮重发历史图片会线性放大存储；扁平布局全局一份，删除时机
// 改由引用计数决定。
func (s *Server) migrateUsageAssetsLayout() {
	root := s.usageAssetsRoot()
	if root == "" || s.store == nil {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("usage assets migration: read root failed: %v", err)
		}
		return
	}
	hasSubdirs := false
	for _, entry := range entries {
		if entry.IsDir() {
			hasSubdirs = true
			break
		}
	}
	if !hasSubdirs {
		return
	}
	log.Printf("usage assets migration: flattening legacy per-request layout under %s", root)
	ctx := context.Background()
	moved := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			log.Printf("usage assets migration: read %s failed: %v", dir, err)
			continue
		}
		for _, file := range files {
			if file.IsDir() {
				continue
			}
			name := file.Name()
			if _, _, ok := parseAssetFileName(name); !ok {
				continue
			}
			src := filepath.Join(dir, name)
			dst := filepath.Join(root, name)
			if _, err := os.Stat(dst); err == nil {
				// 同内容文件已存在（其他请求目录先迁移过）：直接删源。
				_ = os.Remove(src)
				moved++
				continue
			}
			if err := os.Rename(src, dst); err != nil {
				log.Printf("usage assets migration: move %s failed: %v", src, err)
				continue
			}
			moved++
		}
		_ = os.Remove(dir) // 空目录（或仅剩非法名文件）整体清理
	}
	// 从存留记录重建引用：LIKE 预过滤让绝大多数不含占位符的记录零成本跳过。
	// store 是单连接池——必须先把行全部读进内存、释放连接后再写，边读边写
	// 会等待连接死锁（与 rollup 回填同约束）。
	rows, err := s.store.QueryRecordBodiesWithAssets(ctx)
	if err != nil {
		log.Printf("usage assets migration: scan records failed: %v", err)
		return
	}
	type assetRef struct{ requestID, file string }
	var pending []assetRef
	pattern := regexp.MustCompile(`__ELYSIA_ASSET__:([\w-]+)/([0-9a-f]{16}\.[a-z0-9]{1,5})`)
	for rows.Next() {
		var requestID, body string
		if err := rows.Scan(&requestID, &body); err != nil {
			rows.Close()
			log.Printf("usage assets migration: scan row failed: %v", err)
			return
		}
		for _, match := range pattern.FindAllStringSubmatch(body, -1) {
			pending = append(pending, assetRef{requestID, match[2]})
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf("usage assets migration: scan rows failed: %v", err)
	}
	rows.Close()
	refs := 0
	for _, ref := range pending {
		if err := s.store.InsertUsageAssetRef(ctx, ref.requestID, ref.file); err != nil {
			log.Printf("usage assets migration: insert ref failed: %v", err)
			continue
		}
		refs++
	}
	log.Printf("usage assets migration: moved %d file(s), rebuilt %d reference(s)", moved, refs)
}
