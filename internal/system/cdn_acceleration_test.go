package system

import (
	"strings"
	"testing"
)

func TestControlPoint545CDNAcceleration(t *testing.T) {
	t.Run("控制点 5.4.5 功能验证", func(t *testing.T) {
		spec, err := LoadCDNAccelerationSpecFromEnv(func(key string) string {
			switch key {
			case "CDN_ACCEL_ENABLED":
				return "true"
			case "CDN_BASE_URL":
				return "https://cdn.finance.prod.example.com"
			case "CDN_ORIGIN_BASE_URL":
				return "https://assets.finance.prod.example.com"
			case "CDN_IMAGE_PATH_PREFIX":
				return "/assets/images/"
			case "CDN_VIDEO_PATH_PREFIX":
				return "/assets/videos/"
			case "CDN_CACHE_MAX_AGE_SEC":
				return "86400"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("加载 CDN 配置失败: %v", err)
		}
		if !spec.Enabled {
			t.Fatalf("CDN 加速应处于启用状态")
		}
		if spec.CDNBaseURL != "https://cdn.finance.prod.example.com" {
			t.Fatalf("CDN 域名解析错误: %s", spec.CDNBaseURL)
		}
		if spec.OriginBaseURL != "https://assets.finance.prod.example.com" {
			t.Fatalf("回源域名解析错误: %s", spec.OriginBaseURL)
		}
		if spec.ImagePathPrefix != "/assets/images/" || spec.VideoPathPrefix != "/assets/videos/" {
			t.Fatalf("静态资源前缀解析错误: %+v", spec)
		}
		if spec.CacheMaxAgeSeconds != 86400 {
			t.Fatalf("缓存 TTL 解析错误: %d", spec.CacheMaxAgeSeconds)
		}

		rewriteConfig, err := BuildCDNRewriteConfig(spec)
		if err != nil {
			t.Fatalf("生成 CDN 重写配置失败: %v", err)
		}
		requiredConfig := []string{
			"location /assets/images/",
			"location /assets/videos/",
			"proxy_pass https://assets.finance.prod.example.com;",
			`add_header Cache-Control "public, max-age=86400, immutable";`,
		}
		for _, fragment := range requiredConfig {
			if !strings.Contains(rewriteConfig, fragment) {
				t.Fatalf("CDN 重写配置缺少关键片段 %q: %s", fragment, rewriteConfig)
			}
		}

		imageURL, err := ResolveCDNAssetURL(spec, "https://assets.finance.prod.example.com/assets/images/banner.png?v=20260220")
		if err != nil {
			t.Fatalf("图片 URL CDN 重写失败: %v", err)
		}
		if imageURL != "https://cdn.finance.prod.example.com/assets/images/banner.png?v=20260220" {
			t.Fatalf("图片 URL CDN 重写结果错误: %s", imageURL)
		}

		videoURL, err := ResolveCDNAssetURL(spec, "/assets/videos/intro.mp4")
		if err != nil {
			t.Fatalf("视频 URL CDN 重写失败: %v", err)
		}
		if videoURL != "https://cdn.finance.prod.example.com/assets/videos/intro.mp4" {
			t.Fatalf("视频 URL CDN 重写结果错误: %s", videoURL)
		}

		nonStaticURL, err := ResolveCDNAssetURL(spec, "https://assets.finance.prod.example.com/docs/ops-manual.pdf")
		if err != nil {
			t.Fatalf("非静态资源 URL 处理失败: %v", err)
		}
		if nonStaticURL != "https://assets.finance.prod.example.com/docs/ops-manual.pdf" {
			t.Fatalf("非静态资源 URL 不应被重写: %s", nonStaticURL)
		}

		logLine := BuildCDNAccelerationLog(spec)
		requiredLog := []string{
			"CDN 加速已启用",
			"cdn=https://cdn.finance.prod.example.com",
			"origin=https://assets.finance.prod.example.com",
			"image_prefix=/assets/images/",
			"video_prefix=/assets/videos/",
			"cache_max_age=86400s",
		}
		for _, fragment := range requiredLog {
			if !strings.Contains(logLine, fragment) {
				t.Fatalf("CDN 日志缺少关键片段 %q: %s", fragment, logLine)
			}
		}
	})
}

func TestLoadCDNAccelerationSpecFromEnvFallback(t *testing.T) {
	spec, err := LoadCDNAccelerationSpecFromEnv(func(key string) string {
		switch key {
		case "CDN_ACCEL_ENABLED":
			return "maybe"
		case "CDN_BASE_URL":
			return "ftp://cdn.finance.prod.example.com"
		case "CDN_IMAGE_PATH_PREFIX":
			return " "
		case "CDN_CACHE_MAX_AGE_SEC":
			return "bad"
		default:
			return ""
		}
	})
	if err == nil {
		t.Fatalf("非法 CDN 配置应返回回退告警")
	}
	if !strings.Contains(err.Error(), "CDN_ACCEL_ENABLED") {
		t.Fatalf("错误信息应包含 CDN_ACCEL_ENABLED: %v", err)
	}
	if !strings.Contains(err.Error(), "CDN_BASE_URL") {
		t.Fatalf("错误信息应包含 CDN_BASE_URL: %v", err)
	}
	if !strings.Contains(err.Error(), "CDN_CACHE_MAX_AGE_SEC") {
		t.Fatalf("错误信息应包含 CDN_CACHE_MAX_AGE_SEC: %v", err)
	}
	if spec.Enabled != DefaultCDNAccelerationEnabled {
		t.Fatalf("非法启用开关应回退默认值: %t", spec.Enabled)
	}
	if spec.CDNBaseURL != DefaultCDNAccelerationBaseURL {
		t.Fatalf("非法 CDN 域名应回退默认值: %s", spec.CDNBaseURL)
	}
	if spec.CacheMaxAgeSeconds != DefaultCDNAccelerationCacheMaxAgeSeconds {
		t.Fatalf("非法缓存 TTL 应回退默认值: %d", spec.CacheMaxAgeSeconds)
	}
}

func TestResolveCDNAssetURLSkipsThirdPartyDomain(t *testing.T) {
	resolved, err := ResolveCDNAssetURL(
		CDNAccelerationSpec{
			Enabled:         true,
			CDNBaseURL:      "https://cdn.finance.prod.example.com",
			OriginBaseURL:   "https://assets.finance.prod.example.com",
			ImagePathPrefix: "/assets/images/",
			VideoPathPrefix: "/assets/videos/",
		},
		"https://third-party.example.com/assets/images/banner.png",
	)
	if err != nil {
		t.Fatalf("第三方 URL 处理失败: %v", err)
	}
	if resolved != "https://third-party.example.com/assets/images/banner.png" {
		t.Fatalf("第三方域名不应重写: %s", resolved)
	}
}

func TestBuildCDNAccelerationLogDisabled(t *testing.T) {
	spec := DefaultCDNAccelerationSpec()
	spec.Enabled = false
	logLine := BuildCDNAccelerationLog(spec)
	if !strings.Contains(logLine, "CDN 加速未启用") {
		t.Fatalf("关闭日志应提示未启用: %s", logLine)
	}
}
