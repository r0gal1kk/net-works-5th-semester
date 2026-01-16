package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"snakes/pkg/game"
	"snakes/pkg/protocol"
)

type GameRenderer struct {
	widget.BaseWidget

	client       *game.Client
	baseCellSize float32
}

func NewGameRenderer(client *game.Client, baseCellSize float32) *GameRenderer {
	renderer := &GameRenderer{
		client:       client,
		baseCellSize: baseCellSize,
	}
	renderer.ExtendBaseWidget(renderer)
	return renderer
}

func (g *GameRenderer) CreateRenderer() fyne.WidgetRenderer {
	return &gameWidgetRenderer{
		gameRenderer: g,
		objects:      []fyne.CanvasObject{},
	}
}

type gameWidgetRenderer struct {
	gameRenderer *GameRenderer
	objects      []fyne.CanvasObject
}

func (r *gameWidgetRenderer) Layout(size fyne.Size) {
	// Layout handled by container
}

func (r *gameWidgetRenderer) calculateCellSize(width, height int32) float32 {
	cellSize := r.gameRenderer.baseCellSize

	if width > 40 || height > 30 {
		scaleX := float32(40) / float32(width)
		scaleY := float32(30) / float32(height)
		scale := scaleX
		if scaleY < scale {
			scale = scaleY
		}
		cellSize = r.gameRenderer.baseCellSize * scale
		if cellSize < 8 {
			cellSize = 8
		}
	}
	if width < 20 && height < 15 {
		scaleX := float32(40) / float32(width)
		scaleY := float32(30) / float32(height)
		scale := scaleX
		if scaleY < scale {
			scale = scaleY
		}
		cellSize = r.gameRenderer.baseCellSize * scale
		if cellSize > 40 {
			cellSize = 40
		}
	}

	return cellSize
}

func (r *gameWidgetRenderer) MinSize() fyne.Size {
	var width, height int32 = 40, 30

	if engine := r.gameRenderer.client.GetEngine(); engine != nil {
		config := engine.GetConfig()
		width = config.Width
		height = config.Height
	} else if config := r.gameRenderer.client.GetGameConfig(); config != nil {
		width = config.Width
		height = config.Height
	}

	cellSize := r.calculateCellSize(width, height)

	return fyne.NewSize(
		float32(width)*cellSize,
		float32(height)*cellSize,
	)
}

func (r *gameWidgetRenderer) Refresh() {
	state := r.gameRenderer.client.GetState()
	if state == nil {
		return
	}

	var width, height int32 = 40, 30

	if engine := r.gameRenderer.client.GetEngine(); engine != nil {
		config := engine.GetConfig()
		width = config.Width
		height = config.Height
	} else if config := r.gameRenderer.client.GetGameConfig(); config != nil {
		width = config.Width
		height = config.Height
	}

	r.objects = []fyne.CanvasObject{}

	cellSize := r.calculateCellSize(width, height)

	// Фон
	bg := canvas.NewRectangle(color.RGBA{15, 15, 15, 255})
	bg.Resize(fyne.NewSize(float32(width)*cellSize, float32(height)*cellSize))
	r.objects = append(r.objects, bg)

	// Сетка
	gridColor := color.RGBA{30, 30, 30, 255}
	for i := int32(0); i <= width; i++ {
		line := canvas.NewLine(gridColor)
		line.StrokeWidth = 1
		line.Position1 = fyne.NewPos(float32(i)*cellSize, 0)
		line.Position2 = fyne.NewPos(float32(i)*cellSize, float32(height)*cellSize)
		r.objects = append(r.objects, line)
	}
	for i := int32(0); i <= height; i++ {
		line := canvas.NewLine(gridColor)
		line.StrokeWidth = 1
		line.Position1 = fyne.NewPos(0, float32(i)*cellSize)
		line.Position2 = fyne.NewPos(float32(width)*cellSize, float32(i)*cellSize)
		r.objects = append(r.objects, line)
	}

	for _, food := range state.Foods {
		x := (food.X%width + width) % width
		y := (food.Y%height + height) % height

		foodRect := canvas.NewCircle(color.RGBA{255, 0, 0, 255})
		foodRect.Resize(fyne.NewSize(cellSize*0.6, cellSize*0.6))
		foodRect.Move(fyne.NewPos(
			float32(x)*cellSize+cellSize*0.2,
			float32(y)*cellSize+cellSize*0.2,
		))
		r.objects = append(r.objects, foodRect)
	}

	colors := []color.Color{
		color.RGBA{0, 255, 0, 255},
		color.RGBA{0, 100, 255, 255},
		color.RGBA{255, 200, 0, 255},
		color.RGBA{255, 0, 255, 255},
		color.RGBA{0, 255, 255, 255},
		color.RGBA{255, 128, 0, 255},
		color.RGBA{128, 255, 0, 255},
		color.RGBA{255, 0, 128, 255},
	}

	for _, snake := range state.Snakes {
		cells := getSnakeCells(snake, width, height)
		colorIdx := int(snake.PlayerID-1) % len(colors)
		if colorIdx < 0 {
			colorIdx = 0
		}
		snakeColor := colors[colorIdx]

		if snake.State == protocol.ZOMBIE {
			if c, ok := snakeColor.(color.RGBA); ok {
				c.A = 100
				snakeColor = c
			}
		}

		for i, cell := range cells {
			x := (cell.X%width + width) % width
			y := (cell.Y%height + height) % height

			var rect *canvas.Rectangle
			if i == 0 {
				circle := canvas.NewCircle(snakeColor)
				circle.Resize(fyne.NewSize(cellSize*0.9, cellSize*0.9))
				circle.Move(fyne.NewPos(
					float32(x)*cellSize+cellSize*0.05,
					float32(y)*cellSize+cellSize*0.05,
				))
				r.objects = append(r.objects, circle)

				if snake.State == protocol.ALIVE {
					eyeColor := color.White
					eyeSize := cellSize * 0.15

					leftEye := canvas.NewCircle(eyeColor)
					leftEye.Resize(fyne.NewSize(eyeSize, eyeSize))
					leftEye.Move(fyne.NewPos(
						float32(x)*cellSize+cellSize*0.3,
						float32(y)*cellSize+cellSize*0.3,
					))
					r.objects = append(r.objects, leftEye)

					rightEye := canvas.NewCircle(eyeColor)
					rightEye.Resize(fyne.NewSize(eyeSize, eyeSize))
					rightEye.Move(fyne.NewPos(
						float32(x)*cellSize+cellSize*0.6,
						float32(y)*cellSize+cellSize*0.3,
					))
					r.objects = append(r.objects, rightEye)
				}
			} else {
				rect = canvas.NewRectangle(snakeColor)
				rect.Resize(fyne.NewSize(cellSize*0.8, cellSize*0.8))
				rect.Move(fyne.NewPos(
					float32(x)*cellSize+cellSize*0.1,
					float32(y)*cellSize+cellSize*0.1,
				))
				r.objects = append(r.objects, rect)
			}
		}
	}

}

func (r *gameWidgetRenderer) Destroy() {}

func (r *gameWidgetRenderer) Objects() []fyne.CanvasObject {
	return r.objects
}

func getSnakeCells(snake protocol.Snake, width, height int32) []protocol.Coord {
	if len(snake.Points) == 0 {
		return nil
	}

	firstPoint := protocol.Coord{
		X: (snake.Points[0].X%width + width) % width,
		Y: (snake.Points[0].Y%height + height) % height,
	}
	cells := []protocol.Coord{firstPoint}
	current := firstPoint

	for i := 1; i < len(snake.Points); i++ {
		offset := snake.Points[i]

		dx := int32(0)
		dy := int32(0)

		if offset.X != 0 {
			if offset.X > 0 {
				dx = 1
			} else {
				dx = -1
			}
		}

		if offset.Y != 0 {
			if offset.Y > 0 {
				dy = 1
			} else {
				dy = -1
			}
		}

		steps := abs(offset.X) + abs(offset.Y)
		for j := int32(0); j < steps; j++ {
			current.X += dx
			current.Y += dy
			current.X = (current.X + width) % width
			current.Y = (current.Y + height) % height
			cells = append(cells, current)
		}
	}

	return cells
}

func abs(x int32) int32 {
	if x < 0 {
		return -x
	}
	return x
}

func CreateGameView(client *game.Client) fyne.CanvasObject {
	renderer := NewGameRenderer(client, 20)

	_ = client

	scroll := container.NewScroll(renderer)
	scroll.SetMinSize(fyne.NewSize(800, 600))

	return scroll
}
