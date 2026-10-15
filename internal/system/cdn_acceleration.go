package system

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
)

type CDNAssetKind string

const (
	CDNAssetKindUnknown CDNAssetKind = ""
	CDNAssetKindImage   CDNAssetKind = "image"
	CDNAssetKindVideo   CDNAssetKind = "video"
)

const (
	DefaultCDNAccelerationEnabled            = true
	DefaultCDNAccelerationBaseURL            = "https://cdn.finance.example.com"
	DefaultCDNAccelerationOriginBaseURL      = "https://assets.finance.example.com"
	DefaultCDNAccelerationImagePathPrefix    = "/static/images/"
	DefaultCDNAccelerationVideoPathPrefix    = "/static/videos/"
	DefaultCDNAccelerationCacheMaxAgeSeconds = int32(31536000)
)

type CDNAccelerationSpec struct {
	Enabled            bool
	CDNBaseURL         string
	OriginBaseURL      string
	ImagePathPrefix    string
	VideoPathPrefix    string
	CacheMaxAgeSeconds int32
}

func DefaultCDNAccelerationSpec() CDNAccelerationSpec {
	return CDNAccelerationSpec{
		Enabled:            DefaultCDNAccelerationEnabled,
		CDNBaseURL:         DefaultCDNAccelerationBaseURL,
		OriginBaseURL:      DefaultCDNAccelerationOriginBaseURL,
		ImagePathPrefix:    DefaultCDNAccelerationImagePathPrefix,
		VideoPathPrefix:    DefaultCDNAccelerationVideoPathPrefix,
		CacheMaxAgeSeconds: DefaultCDNAccelerationCacheMaxAgeSeconds,
	}
}

func NormalizeCDNAccelerationSpec(spec CDNAccelerationSpec) (CDNAccelerationSpec, error) {
	normalized := DefaultCDNAccelerationSpec()
	normalized.Enabled = spec.Enabled

	if cdnBaseURLRaw := strings.TrimSpace(spec.CDNBaseURL); cdnBaseURLRaw != "" {
		cdnBaseURL, err := normalizeCDNBaseURL(cdnBaseURLRaw)
		if err != nil {
			return CDNAccelerationSpec{}, fmt.Errorf("cdn base url: %w", err)
		}
		normalized.CDNBaseURL = cdnBaseURL
	}
	if originBaseURLRaw := strings.TrimSpace(spec.OriginBaseURL); originBaseURLRaw != "" {
		originBaseURL, err := normalizeCDNBaseURL(originBaseURLRaw)
		if err != nil {
			return CDNAccelerationSpec{}, fmt.Errorf("origin base url: %w", err)
		}
		normalized.OriginBaseURL = originBaseURL
	}
	if imagePathPrefixRaw := strings.TrimSpace(spec.ImagePathPrefix); imagePathPrefixRaw != "" {
		imagePathPrefix, err := normalizeCDNPathPrefix(imagePathPrefixRaw)
		if err != nil {
			return CDNAccelerationSpec{}, fmt.Errorf("image path prefix: %w", err)
		}
		normalized.ImagePathPrefix = imagePathPrefix
	}
	if videoPathPrefixRaw := strings.TrimSpace(spec.VideoPathPrefix); videoPathPrefixRaw != "" {
		videoPathPrefix, err := normalizeCDNPathPrefix(videoPathPrefixRaw)
		if err != nil {
			return CDNAccelerationSpec{}, fmt.Errorf("video path prefix: %w", err)
		}
		normalized.VideoPathPrefix = videoPathPrefix
	}

	switch {
	case spec.CacheMaxAgeSeconds < 0:
		return CDNAccelerationSpec{}, fmt.Errorf("cache max age seconds must be greater than 0")
	case spec.CacheMaxAgeSeconds > 0:
		normalized.CacheMaxAgeSeconds = spec.CacheMaxAgeSeconds
	}

	if normalized.CDNBaseURL == "" {
		return CDNAccelerationSpec{}, fmt.Errorf("cdn base url must not be empty")
	}
	if normalized.OriginBaseURL == "" {
		return CDNAccelerationSpec{}, fmt.Errorf("origin base url must not be empty")
	}
	if normalized.ImagePathPrefix == "" {
		return CDNAccelerationSpec{}, fmt.Errorf("image path prefix must not be empty")
	}
	if normalized.VideoPathPrefix == "" {
		return CDNAccelerationSpec{}, fmt.Errorf("video path prefix must not be empty")
	}
	if strings.EqualFold(normalized.ImagePathPrefix, normalized.VideoPathPrefix) {
		return CDNAccelerationSpec{}, fmt.Errorf("image path prefix and video path prefix must be different")
	}
	if normalized.CacheMaxAgeSeconds <= 0 {
		return CDNAccelerationSpec{}, fmt.Errorf("cache max age seconds must be greater than 0")
	}
	if sameURLHost(normalized.CDNBaseURL, normalized.OriginBaseURL) {
		return CDNAccelerationSpec{}, fmt.Errorf("cdn base url must be different from origin base url")
	}

	return normalized, nil
}

func LoadCDNAccelerationSpecFromEnv(getenv func(string) string) (CDNAccelerationSpec, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	spec := DefaultCDNAccelerationSpec()
	warnings := make([]string, 0, 6)

	enabledRaw := strings.TrimSpace(getenv("CDN_ACCEL_ENABLED"))
	if enabledRaw != "" {
		enabled, err := parseCDNAccelerationBool(enabledRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"CDN_ACCEL_ENABLED=%q 无效，回退默认值 %t: %v",
					enabledRaw,
					DefaultCDNAccelerationEnabled,
					err,
				),
			)
		} else {
			spec.Enabled = enabled
		}
	}

	cdnBaseURLRaw := strings.TrimSpace(getenv("CDN_BASE_URL"))
	if cdnBaseURLRaw != "" {
		cdnBaseURL, err := normalizeCDNBaseURL(cdnBaseURLRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"CDN_BASE_URL=%q 无效，回退默认值 %s: %v",
					cdnBaseURLRaw,
					DefaultCDNAccelerationBaseURL,
					err,
				),
			)
		} else {
			spec.CDNBaseURL = cdnBaseURL
		}
	}

	originBaseURLRaw := strings.TrimSpace(getenv("CDN_ORIGIN_BASE_URL"))
	if originBaseURLRaw != "" {
		originBaseURL, err := normalizeCDNBaseURL(originBaseURLRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"CDN_ORIGIN_BASE_URL=%q 无效，回退默认值 %s: %v",
					originBaseURLRaw,
					DefaultCDNAccelerationOriginBaseURL,
					err,
				),
			)
		} else {
			spec.OriginBaseURL = originBaseURL
		}
	}

	imagePathPrefixRaw := strings.TrimSpace(getenv("CDN_IMAGE_PATH_PREFIX"))
	if imagePathPrefixRaw != "" {
		imagePathPrefix, err := normalizeCDNPathPrefix(imagePathPrefixRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"CDN_IMAGE_PATH_PREFIX=%q 无效，回退默认值 %s: %v",
					imagePathPrefixRaw,
					DefaultCDNAccelerationImagePathPrefix,
					err,
				),
			)
		} else {
			spec.ImagePathPrefix = imagePathPrefix
		}
	}

	videoPathPrefixRaw := strings.TrimSpace(getenv("CDN_VIDEO_PATH_PREFIX"))
	if videoPathPrefixRaw != "" {
		videoPathPrefix, err := normalizeCDNPathPrefix(videoPathPrefixRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"CDN_VIDEO_PATH_PREFIX=%q 无效，回退默认值 %s: %v",
					videoPathPrefixRaw,
					DefaultCDNAccelerationVideoPathPrefix,
					err,
				),
			)
		} else {
			spec.VideoPathPrefix = videoPathPrefix
		}
	}

	cacheMaxAgeRaw := strings.TrimSpace(getenv("CDN_CACHE_MAX_AGE_SEC"))
	if cacheMaxAgeRaw != "" {
		cacheMaxAge, err := parsePositiveInt32(cacheMaxAgeRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"CDN_CACHE_MAX_AGE_SEC=%q 无效，回退默认值 %d: %v",
					cacheMaxAgeRaw,
					DefaultCDNAccelerationCacheMaxAgeSeconds,
					err,
				),
			)
		} else {
			spec.CacheMaxAgeSeconds = cacheMaxAge
		}
	}

	normalized, err := NormalizeCDNAccelerationSpec(spec)
	if err != nil {
		return DefaultCDNAccelerationSpec(), fmt.Errorf("CDN 加速配置非法，回退默认值: %w", err)
	}
	if len(warnings) > 0 {
		return normalized, errors.New(strings.Join(warnings, "; "))
	}
	return normalized, nil
}

func BuildCDNRewriteConfig(spec CDNAccelerationSpec) (string, error) {
	normalized, err := NormalizeCDNAccelerationSpec(spec)
	if err != nil {
		return "", err
	}

	originBaseURL, _ := url.Parse(normalized.OriginBaseURL)
	config := fmt.Sprintf(`location %s {
  proxy_pass %s;
  proxy_set_header Host %s;
  expires %ds;
  add_header Cache-Control "public, max-age=%d, immutable";
}

location %s {
  proxy_pass %s;
  proxy_set_header Host %s;
  expires %ds;
  add_header Cache-Control "public, max-age=%d, immutable";
}
`,
		normalized.ImagePathPrefix,
		normalized.OriginBaseURL,
		originBaseURL.Host,
		normalized.CacheMaxAgeSeconds,
		normalized.CacheMaxAgeSeconds,
		normalized.VideoPathPrefix,
		normalized.OriginBaseURL,
		originBaseURL.Host,
		normalized.CacheMaxAgeSeconds,
		normalized.CacheMaxAgeSeconds,
	)
	return config, nil
}

func ResolveCDNAssetURL(spec CDNAccelerationSpec, rawAssetURL string) (string, error) {
	normalized, err := NormalizeCDNAccelerationSpec(spec)
	if err != nil {
		return "", err
	}

	assetURL := strings.TrimSpace(rawAssetURL)
	if assetURL == "" {
		return "", fmt.Errorf("asset url must not be empty")
	}
	parsed, err := url.Parse(assetURL)
	if err != nil {
		return "", fmt.Errorf("asset url parse failed: %w", err)
	}

	isAbsoluteURL := strings.TrimSpace(parsed.Scheme) != "" || strings.TrimSpace(parsed.Host) != ""
	if isAbsoluteURL {
		if strings.TrimSpace(parsed.Scheme) == "" || strings.TrimSpace(parsed.Host) == "" {
			return "", fmt.Errorf("asset url must include scheme and host")
		}
	} else if !strings.HasPrefix(parsed.Path, "/") {
		parsed.Path = "/" + parsed.Path
	}

	if classifyCDNAssetPath(normalized, parsed.Path) == CDNAssetKindUnknown {
		return parsed.String(), nil
	}
	if !normalized.Enabled {
		return parsed.String(), nil
	}

	cdnBaseURL, _ := url.Parse(normalized.CDNBaseURL)
	if isAbsoluteURL {
		if sameURLHost(parsed.Host, cdnBaseURL.Host) {
			parsed.Scheme = cdnBaseURL.Scheme
			parsed.Host = cdnBaseURL.Host
			parsed.User = nil
			return parsed.String(), nil
		}

		originBaseURL, _ := url.Parse(normalized.OriginBaseURL)
		if !sameURLHost(parsed.Host, originBaseURL.Host) {
			return parsed.String(), nil
		}
	}

	parsed.Scheme = cdnBaseURL.Scheme
	parsed.Host = cdnBaseURL.Host
	parsed.User = nil
	return parsed.String(), nil
}

func BuildCDNAccelerationLog(spec CDNAccelerationSpec) string {
	normalized, err := NormalizeCDNAccelerationSpec(spec)
	if err != nil {
		return fmt.Sprintf("CDN 加速未启用：配置非法 %v", err)
	}
	if !normalized.Enabled {
		return "CDN 加速未启用：CDN_ACCEL_ENABLED=false"
	}
	return fmt.Sprintf(
		"CDN 加速已启用：cdn=%s origin=%s image_prefix=%s video_prefix=%s cache_max_age=%ds",
		normalized.CDNBaseURL,
		normalized.OriginBaseURL,
		normalized.ImagePathPrefix,
		normalized.VideoPathPrefix,
		normalized.CacheMaxAgeSeconds,
	)
}

func classifyCDNAssetPath(spec CDNAccelerationSpec, rawPath string) CDNAssetKind {
	pathText := strings.ToLower(strings.TrimSpace(rawPath))
	if pathText == "" {
		return CDNAssetKindUnknown
	}
	if !strings.HasPrefix(pathText, "/") {
		pathText = "/" + pathText
	}
	imagePrefix := strings.ToLower(strings.TrimSpace(spec.ImagePathPrefix))
	if hasCDNPathPrefix(pathText, imagePrefix) {
		return CDNAssetKindImage
	}
	videoPrefix := strings.ToLower(strings.TrimSpace(spec.VideoPathPrefix))
	if hasCDNPathPrefix(pathText, videoPrefix) {
		return CDNAssetKindVideo
	}
	return CDNAssetKindUnknown
}

func hasCDNPathPrefix(pathText, prefix string) bool {
	if pathText == "" || prefix == "" {
		return false
	}
	if strings.HasPrefix(pathText, prefix) {
		return true
	}
	return strings.TrimSuffix(pathText, "/") == strings.TrimSuffix(prefix, "/")
}

func normalizeCDNBaseURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("must not be empty")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", err
	}
	parsed.Scheme = strings.ToLower(strings.TrimSpace(parsed.Scheme))
	parsed.Host = strings.ToLower(strings.TrimSpace(parsed.Host))
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("must include scheme and host")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("scheme must be http or https")
	}
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.User = nil
	return strings.TrimRight(parsed.String(), "/"), nil
}

func normalizeCDNPathPrefix(raw string) (string, error) {
	prefix := strings.TrimSpace(raw)
	if prefix == "" {
		return "", fmt.Errorf("must not be empty")
	}
	prefix = strings.ReplaceAll(prefix, "\\", "/")
	if strings.ContainsAny(prefix, "?#") {
		return "", fmt.Errorf("must not contain query or fragment")
	}
	if strings.ContainsAny(prefix, " \t\r\n") {
		return "", fmt.Errorf("must not contain spaces")
	}
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	for strings.Contains(prefix, "//") {
		prefix = strings.ReplaceAll(prefix, "//", "/")
	}
	if prefix == "/" {
		return "", fmt.Errorf("must not be root path")
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return prefix, nil
}

func sameURLHost(left, right string) bool {
	leftHost := strings.ToLower(strings.TrimSpace(left))
	rightHost := strings.ToLower(strings.TrimSpace(right))
	if parsed, err := url.Parse(leftHost); err == nil && parsed.Host != "" {
		leftHost = strings.ToLower(strings.TrimSpace(parsed.Host))
	}
	if parsed, err := url.Parse(rightHost); err == nil && parsed.Host != "" {
		rightHost = strings.ToLower(strings.TrimSpace(parsed.Host))
	}
	return leftHost == rightHost && leftHost != ""
}

func parseCDNAccelerationBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "on", "yes", "enable", "enabled":
		return true, nil
	case "0", "false", "off", "no", "disable", "disabled":
		return false, nil
	default:
		return false, fmt.Errorf("must be one of true/false/1/0/on/off/yes/no")
	}
}
