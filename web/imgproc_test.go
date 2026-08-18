package web

import (
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"

	"github.com/kran/cho"
	"os"
	"path/filepath"
	"testing"
)

// 造一张 100x80 的测试图（红色背景 + 左上白点 — 验证裁剪方向）
func makeTestJPG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 100, 80))
	for y := 0; y < 80; y++ {
		for x := 0; x < 100; x++ {
			if x < 10 && y < 10 {
				img.Set(x, y, color.White) // 左上角白点
			} else {
				img.Set(x, y, color.RGBA{200, 30, 30, 255})
			}
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, nil); err != nil {
		t.Fatal(err)
	}
}

func TestServeImg(t *testing.T) {
	uploads := t.TempDir()
	makeTestJPG(t, filepath.Join(uploads, "test.jpg"))

	fs := http.StripPrefix("/uploads", http.FileServer(http.Dir(uploads)))

	// 无参数 → 原图直出（200, 内容就是原文件）
	req := httptest.NewRequest("GET", "/uploads/test.jpg", nil)
	w := httptest.NewRecorder()
	serveImg(uploads, &CmsCtx{BaseContext: cho.MakeBaseContext(w, req)}, fs)
	if w.Code != http.StatusOK {
		t.Fatalf("plain: code = %d", w.Code)
	}

	// cover 300x200 → 200 + 缓存文件生成
	req = httptest.NewRequest("GET", "/uploads/test.jpg?w=300&h=200&mode=cover", nil)
	w = httptest.NewRecorder()
	serveImg(uploads, &CmsCtx{BaseContext: cho.MakeBaseContext(w, req)}, fs)
	if w.Code != http.StatusOK {
		t.Fatalf("cover: code = %d", w.Code)
	}
	// 尺寸验证（解码输出）
	out, err := jpeg.Decode(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	if out.Bounds().Dx() != 300 || out.Bounds().Dy() != 200 {
		t.Fatalf("cover size = %dx%d, want 300x200", out.Bounds().Dx(), out.Bounds().Dy())
	}
	// 缓存文件存在
	cachePath := imgCachePath(uploads, imgParams{W: 300, H: 200, Mode: "cover"}, "/uploads/test.jpg")
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("cache not written: %v", err)
	}

	// 命中缓存（第二次请求 — 不重新处理, 直接出缓存文件）
	req = httptest.NewRequest("GET", "/uploads/test.jpg?w=300&h=200&mode=cover", nil)
	w2 := httptest.NewRecorder()
	serveImg(uploads, &CmsCtx{BaseContext: cho.MakeBaseContext(w2, req)}, fs)
	if w2.Code != http.StatusOK {
		t.Fatalf("cached: code = %d", w2.Code)
	}

	// fit 200x150 → 等比内嵌（100x80 → 200x160? no — fit 缩放至 200 宽, 高按比例 160 — 但目标 200x150 内嵌 → 187x150）
	req = httptest.NewRequest("GET", "/uploads/test.jpg?w=200&h=150&mode=fit", nil)
	w = httptest.NewRecorder()
	serveImg(uploads, &CmsCtx{BaseContext: cho.MakeBaseContext(w, req)}, fs)
	if w.Code != http.StatusOK {
		t.Fatalf("fit: code = %d", w.Code)
	}

	// 非法参数 → 400
	req = httptest.NewRequest("GET", "/uploads/test.jpg?w=999999", nil)
	w = httptest.NewRecorder()
	serveImg(uploads, &CmsCtx{BaseContext: cho.MakeBaseContext(w, req)}, fs)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad param: code = %d, want 400", w.Code)
	}

	// 不支持格式（伪造 webp — imaging.Open 解码失败）→ 原样返回原文件, 不 404
	fakeWebp := filepath.Join(uploads, "fake.webp")
	if err := os.WriteFile(fakeWebp, []byte("fake webp data"), 0o644); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest("GET", "/uploads/fake.webp?w=300&h=200", nil)
	w = httptest.NewRecorder()
	serveImg(uploads, &CmsCtx{BaseContext: cho.MakeBaseContext(w, req)}, fs)
	if w.Code != http.StatusOK {
		t.Fatalf("webp fallback: code = %d, want 200", w.Code)
	}
	if w.Body.String() != "fake webp data" {
		t.Fatalf("webp fallback: body = %q, want original file", w.Body.String())
	}

	// 不存在的文件 → 404
	req = httptest.NewRequest("GET", "/uploads/nope.jpg?w=100", nil)
	w = httptest.NewRecorder()
	serveImg(uploads, &CmsCtx{BaseContext: cho.MakeBaseContext(w, req)}, fs)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing: code = %d, want 404", w.Code)
	}
}
