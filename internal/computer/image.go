package computer

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
)

// 留出 base64 膨胀和 MCP 元数据空间；两端 Bridge 单条消息上限为 8 MiB。
const imageBudget = 4 << 20

func fitImage(ctx context.Context, shot Snapshot, budget int) (Snapshot, error) {
	if len(shot.PNG) <= budget {
		return shot, nil
	}
	source, err := png.Decode(bytes.NewReader(shot.PNG))
	if err != nil {
		return Snapshot{}, fmt.Errorf("decode native screenshot: %w", err)
	}
	originalWidth, originalHeight := shot.Width, shot.Height
	width, height := originalWidth, originalHeight
	for len(shot.PNG) > budget {
		if err = ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		if width == 1 && height == 1 {
			return Snapshot{}, fmt.Errorf("image budget cannot hold a PNG")
		}
		width = max(1, width*3/4)
		height = max(1, height*3/4)
		resized := image.NewRGBA(image.Rect(0, 0, width, height))
		bounds := source.Bounds()
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				resized.Set(x, y, source.At(bounds.Min.X+x*bounds.Dx()/width, bounds.Min.Y+y*bounds.Dy()/height))
			}
		}
		var encoded bytes.Buffer
		if err = png.Encode(&encoded, resized); err != nil {
			return Snapshot{}, err
		}
		shot.PNG = encoded.Bytes()
	}
	sx, sy := float64(originalWidth)/float64(width), float64(originalHeight)/float64(height)
	shot.Transform[0] *= sx
	shot.Transform[1] *= sx
	shot.Transform[2] *= sy
	shot.Transform[3] *= sy
	shot.Width, shot.Height = width, height
	return shot, nil
}
