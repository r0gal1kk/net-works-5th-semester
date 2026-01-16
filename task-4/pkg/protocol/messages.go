package protocol

type Coord struct {
	X int32
	Y int32
}

type GamePlayer struct {
	Name      string
	ID        int32
	IPAddress string
	Port      int32
	Role      NodeRole
	Type      PlayerType
	Score     int32
}

type GameConfig struct {
	Width        int32
	Height       int32
	FoodStatic   int32
	StateDelayMs int32
}

type Snake struct {
	PlayerID      int32
	Points        []Coord
	State         SnakeState
	HeadDirection Direction
}

type GameState struct {
	StateOrder int32
	Snakes     []Snake
	Foods      []Coord
	Players    []GamePlayer
}

type GameAnnouncement struct {
	Players  []GamePlayer
	Config   GameConfig
	CanJoin  bool
	GameName string
}
