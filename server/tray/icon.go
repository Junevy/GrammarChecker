package tray

// 动态生成托盘图标（32x32 蓝底白勾 ICO），避免引入二进制资源文件。
//
// ICO 结构：ICONDIR(6B) + ICONDIRENTRY(16B) + BITMAPINFOHEADER(40B)
// + BGRA 像素（自下而上）+ AND 掩码（全 0，透明度由 alpha 通道决定）。

import "math"

// iconICO 生成 32x32 图标的 ICO 字节流。
func iconICO() []byte {
	const size = 32
	px := make([]byte, size*size*4) // BGRA，自下而上

	// 蓝色圆底（主蓝 #0066CC，边缘 0.7px 抗锯齿过渡到透明）
	const r = 15.5
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			d := math.Hypot(float64(x)-15.5, float64(y)-15.5)
			switch {
			case d <= r-0.7:
				setPx(px, size, x, y, 0xCC, 0x66, 0x00, 255)
			case d <= r:
				a := byte(255 * (r - d) / 0.7)
				setPx(px, size, x, y, 0xCC, 0x66, 0x00, a)
			}
		}
	}

	// 白色对勾：(8,17) → (14,23) → (24,10)，总线宽约 3px
	drawSegment(px, size, 8, 17, 14, 23)
	drawSegment(px, size, 14, 23, 24, 10)

	return buildICO(px, size)
}

// setPx 写入一个像素（BGRA 顺序，BMP 行序自下而上）。
func setPx(px []byte, size, x, y int, b, g, r, a byte) {
	if x < 0 || y < 0 || x >= size || y >= size {
		return
	}
	i := ((size-1-y)*size + x) * 4
	px[i], px[i+1], px[i+2], px[i+3] = b, g, r, a
}

// drawSegment 沿线段画白色粗线（3x3 邻域内距中心 ≤1.4 的像素着色）。
func drawSegment(px []byte, size int, x0, y0, x1, y1 int) {
	const halfW = 1.4
	dx, dy := float64(x1-x0), float64(y1-y0)
	steps := int(math.Hypot(dx, dy) * 4)
	if steps < 1 {
		steps = 1
	}
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		cx, cy := float64(x0)+dx*t, float64(y0)+dy*t
		for oy := -1; oy <= 1; oy++ {
			for ox := -1; ox <= 1; ox++ {
				if math.Hypot(float64(ox), float64(oy)) <= halfW {
					setPx(px, size, int(cx)+ox, int(cy)+oy, 255, 255, 255, 255)
				}
			}
		}
	}
}

// buildICO 组装 ICO 容器字节流。
func buildICO(px []byte, size int) []byte {
	maskRow := size / 8                      // AND 掩码每行字节数（32px → 4B）
	mask := make([]byte, maskRow*size)       // 全 0：完全依赖 alpha 通道
	total := uint32(40 + len(px) + len(mask))

	out := make([]byte, 0, 22+int(total))
	// ICONDIR：保留 0 / 类型 1（图标）/ 数量 1
	out = append(out, 0, 0, 1, 0, 1, 0)
	// ICONDIRENTRY：宽 / 高 / 色板数 / 保留 / 色板 / 位深 / 数据大小 / 数据偏移
	out = append(out, byte(size), byte(size), 0, 0, 1, 0,
		byte(total), byte(total>>8), byte(total>>16), byte(total>>24),
		22, 0, 0, 0)
	// BITMAPINFOHEADER：biHeight = 2*size（XOR 像素 + AND 掩码两段）
	hdr := make([]byte, 40)
	hdr[0] = 40 // biSize
	hdr[4] = byte(size)
	hdr[8] = byte(2 * size)
	hdr[12] = 1 // biPlanes
	hdr[14] = 32 // biBitCount
	out = append(out, hdr...)
	out = append(out, px...)
	return append(out, mask...)
}
