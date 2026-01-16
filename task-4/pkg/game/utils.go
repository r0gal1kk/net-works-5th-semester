package game

import (
	"math/rand"

	"snakes/pkg/protocol"
)

func randomDirection() protocol.Direction {
	directions := []protocol.Direction{protocol.UP, protocol.DOWN, protocol.LEFT, protocol.RIGHT}
	return directions[rand.Intn(len(directions))]
}

func oppositeDirection(dir protocol.Direction) protocol.Direction {
	switch dir {
	case protocol.UP:
		return protocol.DOWN
	case protocol.DOWN:
		return protocol.UP
	case protocol.LEFT:
		return protocol.RIGHT
	case protocol.RIGHT:
		return protocol.LEFT
	default:
		return protocol.UP
	}
}

func isOppositeDirection(dir1, dir2 protocol.Direction) bool {
	return oppositeDirection(dir1) == dir2
}

func directionToOffset(dir protocol.Direction) protocol.Coord {
	switch dir {
	case protocol.UP:
		return protocol.Coord{X: 0, Y: -1}
	case protocol.DOWN:
		return protocol.Coord{X: 0, Y: 1}
	case protocol.LEFT:
		return protocol.Coord{X: -1, Y: 0}
	case protocol.RIGHT:
		return protocol.Coord{X: 1, Y: 0}
	default:
		return protocol.Coord{X: 0, Y: 0}
	}
}

func getSnakeCells(snake protocol.Snake, width, height int32) []protocol.Coord {
	if len(snake.Points) == 0 {
		return nil
	}

	cells := []protocol.Coord{snake.Points[0]}
	current := snake.Points[0]

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

		steps := absInt32(offset.X) + absInt32(offset.Y)
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

func cellsToKeyPointsWithSize(cells []protocol.Coord, width, height int32) []protocol.Coord {
	if len(cells) == 0 {
		return nil
	}

	points := []protocol.Coord{cells[0]}

	if len(cells) == 1 {
		return points
	}

	prevCell := cells[0]
	currentOffset := protocol.Coord{
		X: cells[1].X - cells[0].X,
		Y: cells[1].Y - cells[0].Y,
	}

	normalizeOffset(&currentOffset, width, height)
	accumulatedOffset := currentOffset
	prevCell = cells[1]

	for i := 2; i < len(cells); i++ {
		nextOffset := protocol.Coord{
			X: cells[i].X - prevCell.X,
			Y: cells[i].Y - prevCell.Y,
		}
		normalizeOffset(&nextOffset, width, height)

		if isSameDirection(currentOffset, nextOffset) {
			accumulatedOffset.X += nextOffset.X
			accumulatedOffset.Y += nextOffset.Y
		} else {
			points = append(points, accumulatedOffset)
			currentOffset = nextOffset
			accumulatedOffset = nextOffset
		}
		prevCell = cells[i]
	}

	points = append(points, accumulatedOffset)

	return points
}

func normalizeOffset(offset *protocol.Coord, width, height int32) {
	if offset.X > width/2 {
		offset.X = offset.X - width
	} else if offset.X < -width/2 {
		offset.X = offset.X + width
	}
	if offset.Y > height/2 {
		offset.Y = offset.Y - height
	} else if offset.Y < -height/2 {
		offset.Y = offset.Y + height
	}
}

func isSameDirection(a, b protocol.Coord) bool {
	aDirX := sign(a.X)
	aDirY := sign(a.Y)
	bDirX := sign(b.X)
	bDirY := sign(b.Y)

	return aDirX == bDirX && aDirY == bDirY
}

func sign(x int32) int32 {
	if x > 0 {
		return 1
	} else if x < 0 {
		return -1
	}
	return 0
}

func absInt32(x int32) int32 {
	if x < 0 {
		return -x
	}
	return x
}
