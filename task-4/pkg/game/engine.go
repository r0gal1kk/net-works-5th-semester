package game

import (
	"math/rand"
	"sync"
	"time"

	"snakes/pkg/protocol"
)

type SteerRequest struct {
	Direction protocol.Direction
	MsgSeq    int64
}

type Engine struct {
	config       *protocol.GameConfig
	state        *protocol.GameState
	gameName     string
	nextPlayerID int32
	mu           sync.RWMutex

	steerRequests map[int32]*SteerRequest
	steerMu       sync.Mutex

	deadPlayers   []int32
	deadPlayersMu sync.Mutex
}

func NewEngine(config *protocol.GameConfig, gameName string) *Engine {
	return &Engine{
		config:        config,
		state:         &protocol.GameState{StateOrder: 0},
		gameName:      gameName,
		nextPlayerID:  1,
		steerRequests: make(map[int32]*SteerRequest),
	}
}

func NewEngineFromState(config *protocol.GameConfig, gameName string, state *protocol.GameState) *Engine {
	maxID := int32(0)

	for _, player := range state.Players {
		if player.ID > maxID {
			maxID = player.ID
		}
	}

	for _, snake := range state.Snakes {
		if snake.PlayerID > maxID {
			maxID = snake.PlayerID
		}
	}

	stateCopy := &protocol.GameState{
		StateOrder: state.StateOrder,
		Players:    make([]protocol.GamePlayer, len(state.Players)),
		Snakes:     make([]protocol.Snake, len(state.Snakes)),
		Foods:      make([]protocol.Coord, len(state.Foods)),
	}
	copy(stateCopy.Players, state.Players)
	copy(stateCopy.Snakes, state.Snakes)
	copy(stateCopy.Foods, state.Foods)

	return &Engine{
		config:        config,
		state:         stateCopy,
		gameName:      gameName,
		nextPlayerID:  maxID + 10,
		steerRequests: make(map[int32]*SteerRequest),
	}
}

func (e *Engine) AddPlayer(name string, role protocol.NodeRole, playerType protocol.PlayerType) (*protocol.GamePlayer, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	player := protocol.GamePlayer{
		ID:    e.nextPlayerID,
		Name:  name,
		Role:  role,
		Type:  playerType,
		Score: 0,
	}
	e.nextPlayerID++

	e.state.Players = append(e.state.Players, player)

	if role != protocol.VIEWER {
		snake, err := e.createSnake(player.ID)
		if err != nil {
			e.state.Players = e.state.Players[:len(e.state.Players)-1]
			return nil, err
		}
		e.state.Snakes = append(e.state.Snakes, snake)
	}

	return &player, nil
}

func (e *Engine) createSnake(playerID int32) (protocol.Snake, error) {
	for attempt := 0; attempt < 100; attempt++ {
		x := rand.Int31n(e.config.Width)
		y := rand.Int31n(e.config.Height)

		if e.isSquareFree(x, y, 5) {
			dir := randomDirection()
			tailOffset := oppositeDirection(dir)

			return protocol.Snake{
				PlayerID:      playerID,
				Points:        []protocol.Coord{{X: x, Y: y}, directionToOffset(tailOffset)},
				State:         protocol.ALIVE,
				HeadDirection: dir,
			}, nil
		}
	}

	return protocol.Snake{}, &GameError{"No free space for snake"}
}

func (e *Engine) isSquareFree(centerX, centerY, size int32) bool {
	half := size / 2

	for dy := -half; dy <= half; dy++ {
		for dx := -half; dx <= half; dx++ {
			x := (centerX + dx + e.config.Width) % e.config.Width
			y := (centerY + dy + e.config.Height) % e.config.Height

			for _, snake := range e.state.Snakes {
				cells := getSnakeCells(snake, e.config.Width, e.config.Height)
				for _, cell := range cells {
					if cell.X == x && cell.Y == y {
						return false
					}
				}
			}

			for _, food := range e.state.Foods {
				if food.X == x && food.Y == y {
					return false
				}
			}
		}
	}

	return true
}

func (e *Engine) RequestSteer(playerID int32, direction protocol.Direction, msgSeq int64) {
	e.steerMu.Lock()
	defer e.steerMu.Unlock()
	//проверка старых и новых запросов
	existing, exists := e.steerRequests[playerID]
	if !exists || msgSeq > existing.MsgSeq {
		e.steerRequests[playerID] = &SteerRequest{
			Direction: direction,
			MsgSeq:    msgSeq,
		}
	}
}

func (e *Engine) Update() bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.applySteerRequests()

	e.moveSnakes()

	e.checkCollisions()

	e.updateFood()

	e.state.StateOrder++

	aliveSnakeCount := 0
	for _, snake := range e.state.Snakes {
		if snake.State == protocol.ALIVE {
			aliveSnakeCount++
		}
	}

	return aliveSnakeCount > 0
}

func (e *Engine) applySteerRequests() {
	e.steerMu.Lock()
	defer e.steerMu.Unlock()

	for playerID, steerReq := range e.steerRequests {
		for i := range e.state.Snakes {
			if e.state.Snakes[i].PlayerID == playerID && e.state.Snakes[i].State == protocol.ALIVE {
				if !isOppositeDirection(e.state.Snakes[i].HeadDirection, steerReq.Direction) {
					e.state.Snakes[i].HeadDirection = steerReq.Direction
				}
			}
		}
	}

	e.steerRequests = make(map[int32]*SteerRequest)
}

func (e *Engine) moveSnakes() {
	for i := range e.state.Snakes {
		e.moveSnake(&e.state.Snakes[i])
	}
}

func (e *Engine) moveSnake(snake *protocol.Snake) {
	if len(snake.Points) == 0 {
		return
	}

	offset := directionToOffset(snake.HeadDirection)
	head := protocol.Coord{
		X: (snake.Points[0].X%e.config.Width + e.config.Width) % e.config.Width,
		Y: (snake.Points[0].Y%e.config.Height + e.config.Height) % e.config.Height,
	}
	newHead := protocol.Coord{
		X: (head.X + offset.X + e.config.Width) % e.config.Width,
		Y: (head.Y + offset.Y + e.config.Height) % e.config.Height,
	}

	ateFood := false
	for i, food := range e.state.Foods {
		if food.X == newHead.X && food.Y == newHead.Y {
			ateFood = true
			e.state.Foods = append(e.state.Foods[:i], e.state.Foods[i+1:]...)

			for j := range e.state.Players {
				if e.state.Players[j].ID == snake.PlayerID {
					e.state.Players[j].Score++
					break
				}
			}
			break
		}
	}

	cells := getSnakeCells(*snake, e.config.Width, e.config.Height)

	if ateFood {
		cells = append([]protocol.Coord{newHead}, cells...)
	} else {
		if len(cells) > 1 {
			cells = cells[:len(cells)-1]
		}
		cells = append([]protocol.Coord{newHead}, cells...)
	}

	snake.Points = cellsToKeyPointsWithSize(cells, e.config.Width, e.config.Height)
}

// логика столкновений змей
func (e *Engine) checkCollisions() {

	type snakeHead struct {
		playerID int32
		head     protocol.Coord
		isAlive  bool
	}

	var heads []snakeHead
	for _, snake := range e.state.Snakes {
		if snake.State != protocol.ALIVE && snake.State != protocol.ZOMBIE {
			continue
		}
		if len(snake.Points) == 0 {
			continue
		}
		normalizedHead := protocol.Coord{
			X: (snake.Points[0].X%e.config.Width + e.config.Width) % e.config.Width,
			Y: (snake.Points[0].Y%e.config.Height + e.config.Height) % e.config.Height,
		}
		heads = append(heads, snakeHead{
			playerID: snake.PlayerID,
			head:     normalizedHead,
			isAlive:  true,
		})
	}

	deadPlayerIDs := make(map[int32]bool)
	scoreIncrease := make(map[int32]int32)

	for i := 0; i < len(heads); i++ {
		for j := i + 1; j < len(heads); j++ {
			if heads[i].head.X == heads[j].head.X && heads[i].head.Y == heads[j].head.Y {
				deadPlayerIDs[heads[i].playerID] = true
				deadPlayerIDs[heads[j].playerID] = true
			}
		}
	}

	for _, headInfo := range heads {
		if deadPlayerIDs[headInfo.playerID] {
			continue
		}

		for _, snake := range e.state.Snakes {
			if len(snake.Points) == 0 {
				continue
			}

			cells := getSnakeCells(snake, e.config.Width, e.config.Height)
			if len(cells) == 0 {
				continue
			}

			startIdx := 0
			if snake.PlayerID == headInfo.playerID {
				startIdx = 1
			}

			for k := startIdx; k < len(cells); k++ {
				if cells[k].X == headInfo.head.X && cells[k].Y == headInfo.head.Y {
					deadPlayerIDs[headInfo.playerID] = true

					if snake.PlayerID != headInfo.playerID {
						scoreIncrease[snake.PlayerID]++
					}
					break
				}
			}

			if deadPlayerIDs[headInfo.playerID] {
				break
			}
		}
	}

	for playerID, points := range scoreIncrease {
		for i := range e.state.Players {
			if e.state.Players[i].ID == playerID {
				e.state.Players[i].Score += points
				break
			}
		}
	}

	for playerID := range deadPlayerIDs {
		e.killSnakeByPlayerID(playerID)
	}
}

func (e *Engine) killSnakeByPlayerID(playerID int32) {
	snakeIdx := -1
	for i := range e.state.Snakes {
		if e.state.Snakes[i].PlayerID == playerID {
			snakeIdx = i
			break
		}
	}

	if snakeIdx < 0 {
		return
	}

	snake := e.state.Snakes[snakeIdx]

	cells := getSnakeCells(snake, e.config.Width, e.config.Height)
	for _, cell := range cells {
		if rand.Float32() < 0.5 {
			hasFood := false
			for _, food := range e.state.Foods {
				if food.X == cell.X && food.Y == cell.Y {
					hasFood = true
					break
				}
			}
			if !hasFood {
				e.state.Foods = append(e.state.Foods, cell)
			}
		}
	}

	if snakeIdx < len(e.state.Snakes) {
		e.state.Snakes = append(e.state.Snakes[:snakeIdx], e.state.Snakes[snakeIdx+1:]...)
	}

	for i := range e.state.Players {
		if e.state.Players[i].ID == playerID {
			if e.state.Players[i].Role != protocol.MASTER {
				e.state.Players[i].Role = protocol.VIEWER
			}
			break
		}
	}

	e.addDeadPlayer(playerID)
}

func (e *Engine) MakePlayerZombie(playerID int32) {
	e.mu.Lock()
	defer e.mu.Unlock()

	for i := range e.state.Snakes {
		if e.state.Snakes[i].PlayerID == playerID {
			e.state.Snakes[i].State = protocol.ZOMBIE
			break
		}
	}

	for i := range e.state.Players {
		if e.state.Players[i].ID == playerID {
			e.state.Players[i].Role = protocol.VIEWER
			break
		}
	}
}

func (e *Engine) updateFood() {
	aliveSnakes := int32(0)
	for _, snake := range e.state.Snakes {
		if snake.State == protocol.ALIVE {
			aliveSnakes++
		}
	}

	requiredFood := e.config.FoodStatic + aliveSnakes
	currentFood := int32(len(e.state.Foods))

	for currentFood < requiredFood {
		if e.addRandomFood() {
			currentFood++
		} else {
			break
		}
	}
}

func (e *Engine) addRandomFood() bool {
	for attempt := 0; attempt < 100; attempt++ {
		x := rand.Int31n(e.config.Width)
		y := rand.Int31n(e.config.Height)

		free := true

		for _, snake := range e.state.Snakes {
			cells := getSnakeCells(snake, e.config.Width, e.config.Height)
			for _, cell := range cells {
				if cell.X == x && cell.Y == y {
					free = false
					break
				}
			}
			if !free {
				break
			}
		}

		for _, food := range e.state.Foods {
			if food.X == x && food.Y == y {
				free = false
				break
			}
		}

		if free {
			e.state.Foods = append(e.state.Foods, protocol.Coord{X: x, Y: y})
			return true
		}
	}

	return false
}

func (e *Engine) GetState() *protocol.GameState {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.state
}

func (e *Engine) GetConfig() *protocol.GameConfig {
	return e.config
}

func (e *Engine) GetGameName() string {
	return e.gameName
}

func (e *Engine) CanJoin() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()

	for attempt := 0; attempt < 10; attempt++ {
		x := rand.Int31n(e.config.Width)
		y := rand.Int31n(e.config.Height)
		if e.isSquareFree(x, y, 5) {
			return true
		}
	}

	return false
}

type GameError struct {
	Message string
}

func (e *GameError) Error() string {
	return e.Message
}

func (e *Engine) GetDeadPlayers() []int32 {
	e.deadPlayersMu.Lock()
	defer e.deadPlayersMu.Unlock()

	result := make([]int32, len(e.deadPlayers))
	copy(result, e.deadPlayers)
	e.deadPlayers = nil
	return result
}

func (e *Engine) addDeadPlayer(playerID int32) {
	e.deadPlayersMu.Lock()
	defer e.deadPlayersMu.Unlock()
	e.deadPlayers = append(e.deadPlayers, playerID)
}

func init() {
	rand.Seed(time.Now().UnixNano())
}
