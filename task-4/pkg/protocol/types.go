package protocol

type NodeRole int

const (
	NORMAL NodeRole = 0
	MASTER NodeRole = 1
	DEPUTY NodeRole = 2
	VIEWER NodeRole = 3
)

type PlayerType int

const (
	HUMAN PlayerType = 0
	ROBOT PlayerType = 1
)

type Direction int

const (
	UP    Direction = 1
	DOWN  Direction = 2
	LEFT  Direction = 3
	RIGHT Direction = 4
)

type SnakeState int

const (
	ALIVE  SnakeState = 0
	ZOMBIE SnakeState = 1
)
