package helperapp

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
)

func trayIconPNG() []byte {
	const size = 32
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	drawRoundedBlock(img, image.Rect(5, 5, 15, 15), color.RGBA{R: 35, G: 35, B: 35, A: 255})
	drawRoundedBlock(img, image.Rect(17, 5, 27, 15), color.RGBA{R: 35, G: 35, B: 35, A: 255})
	drawRoundedBlock(img, image.Rect(5, 17, 15, 27), color.RGBA{R: 35, G: 35, B: 35, A: 255})
	drawRoundedBlock(img, image.Rect(17, 17, 27, 27), color.RGBA{R: 35, G: 35, B: 35, A: 255})

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}

func drawRoundedBlock(img draw.Image, rect image.Rectangle, fill color.Color) {
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			if isCornerPixel(x, y, rect) {
				continue
			}
			img.Set(x, y, fill)
		}
	}
}

func isCornerPixel(x, y int, rect image.Rectangle) bool {
	left := x == rect.Min.X
	right := x == rect.Max.X-1
	top := y == rect.Min.Y
	bottom := y == rect.Max.Y-1
	return (left || right) && (top || bottom)
}
