package game

import (
	"fmt"
	"sync"
	"time"

	"snakes/pkg/protocol"
)

type GameInfo struct {
	GameName   string
	Players    []protocol.GamePlayer
	Config     protocol.GameConfig
	CanJoin    bool
	MasterIP   string
	MasterPort int32
	LastSeen   time.Time
}

type Client struct {
	playerName string
	playerID   int32
	role       protocol.NodeRole

	engine     *Engine
	gameConfig *protocol.GameConfig
	gameName   string
	state      *protocol.GameState
	stateMu    sync.RWMutex

	masterIP   string
	masterPort int32
	deputyIP   string
	deputyPort int32
	masterMu   sync.RWMutex

	availableGames map[string]*GameInfo
	gamesMu        sync.RWMutex

	stateListeners      []func(*protocol.GameState)
	gameListListeners   []func(map[string]*GameInfo)
	errorHandler        func(string)
	leaveHandler        func()
	becomeMasterHandler func(*Engine)
	gameOverHandler     func()
	listenerMu          sync.RWMutex

	inGame           bool
	gameActive       bool
	gameOverNotified bool
}

func NewClient(name string) *Client {
	return &Client{
		playerName:     name,
		role:           protocol.NORMAL,
		availableGames: make(map[string]*GameInfo),
	}
}

func (c *Client) GetPlayerName() string {
	return c.playerName
}

func (c *Client) SetPlayerName(name string) {
	c.playerName = name
}

func (c *Client) GetPlayerID() int32 {
	return c.playerID
}

func (c *Client) GetRole() protocol.NodeRole {
	return c.role
}

func (c *Client) SetRole(role protocol.NodeRole) {
	c.role = role
}

func (c *Client) IsMaster() bool {
	return c.role == protocol.MASTER
}

func (c *Client) IsDeputy() bool {
	return c.role == protocol.DEPUTY
}

func (c *Client) IsViewer() bool {
	return c.role == protocol.VIEWER
}

func (c *Client) IsInGame() bool {
	return c.inGame
}

func (c *Client) GetEngine() *Engine {
	return c.engine
}

func (c *Client) SetEngine(engine *Engine) {
	c.engine = engine
}

func (c *Client) GetState() *protocol.GameState {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.state
}

func (c *Client) StartNewGame(config *protocol.GameConfig, gameName string) error {
	c.engine = NewEngine(config, gameName)

	player, err := c.engine.AddPlayer(c.playerName, protocol.MASTER, protocol.HUMAN)
	if err != nil {
		return fmt.Errorf("failed to add player: %w", err)
	}

	c.playerID = player.ID
	c.role = protocol.MASTER
	c.inGame = true
	c.gameActive = true
	c.state = c.engine.GetState()

	c.notifyStateListeners()

	return nil
}

func (c *Client) UpdateState(state *protocol.GameState) {
	if state == nil {
		return
	}

	c.stateMu.Lock()

	if c.IsMaster() {
		c.state = state
		c.stateMu.Unlock()
		c.notifyStateListeners()
		return
	}

	if c.state != nil && state.StateOrder <= c.state.StateOrder {
		c.stateMu.Unlock()
		return
	}
	c.state = state
	c.stateMu.Unlock()

	c.notifyStateListeners()
}

func (c *Client) UpdateAvailableGames(info *GameInfo) {
	c.gamesMu.Lock()
	c.availableGames[info.GameName] = info
	c.gamesMu.Unlock()

	c.notifyGameListListeners()
}

func (c *Client) GetAvailableGames() map[string]*GameInfo {
	c.gamesMu.RLock()
	defer c.gamesMu.RUnlock()

	result := make(map[string]*GameInfo)
	for k, v := range c.availableGames {
		result[k] = v
	}
	return result
}

func (c *Client) CleanupOldGames(timeout time.Duration) {
	c.gamesMu.Lock()
	defer c.gamesMu.Unlock()

	now := time.Now()
	for name, info := range c.availableGames {
		if now.Sub(info.LastSeen) > timeout {
			delete(c.availableGames, name)
		}
	}
}

func (c *Client) SetMasterAddress(ip string, port int32) {
	c.masterMu.Lock()
	c.masterIP = ip
	c.masterPort = port
	c.masterMu.Unlock()
}

func (c *Client) GetMasterAddress() (string, int32) {
	c.masterMu.RLock()
	defer c.masterMu.RUnlock()
	return c.masterIP, c.masterPort
}

func (c *Client) SetDeputyAddress(ip string, port int32) {
	c.masterMu.Lock()
	c.deputyIP = ip
	c.deputyPort = port
	c.masterMu.Unlock()
}

func (c *Client) GetDeputyAddress() (string, int32) {
	c.masterMu.RLock()
	defer c.masterMu.RUnlock()
	return c.deputyIP, c.deputyPort
}

func (c *Client) LeaveGame() {
	if c.role == protocol.MASTER {
		c.listenerMu.RLock()
		handler := c.leaveHandler
		c.listenerMu.RUnlock()

		if handler != nil {
			handler()
		}
	}

	if c.engine != nil && c.playerID != 0 {
		c.engine.MakePlayerZombie(c.playerID)
	}

	c.inGame = false
	c.gameActive = false
	c.gameOverNotified = false
	c.role = protocol.NORMAL
	c.engine = nil
	c.state = nil
	c.playerID = 0

	c.masterMu.Lock()
	c.masterIP = ""
	c.masterPort = 0
	c.deputyIP = ""
	c.deputyPort = 0
	c.masterMu.Unlock()

	c.gameConfig = nil
	c.gameName = ""
}

//обработка выхода из игры

func (c *Client) SetLeaveHandler(handler func()) {
	c.listenerMu.Lock()
	c.leaveHandler = handler
	c.listenerMu.Unlock()
}

func (c *Client) IsGameActive() bool {
	return c.gameActive
}

func (c *Client) SetGameActive(active bool) {
	c.gameActive = active
}

func (c *Client) AddStateListener(listener func(*protocol.GameState)) {
	c.listenerMu.Lock()
	c.stateListeners = append(c.stateListeners, listener)
	c.listenerMu.Unlock()
}

func (c *Client) AddGameListListener(listener func(map[string]*GameInfo)) {
	c.listenerMu.Lock()
	c.gameListListeners = append(c.gameListListeners, listener)
	c.listenerMu.Unlock()
}

func (c *Client) SetErrorHandler(handler func(string)) {
	c.listenerMu.Lock()
	c.errorHandler = handler
	c.listenerMu.Unlock()
}

func (c *Client) NotifyError(message string) {
	c.listenerMu.RLock()
	handler := c.errorHandler
	c.listenerMu.RUnlock()

	if handler != nil {
		handler(message)
	}
}

func (c *Client) SetBecomeMasterHandler(handler func(*Engine)) {
	c.listenerMu.Lock()
	c.becomeMasterHandler = handler
	c.listenerMu.Unlock()
}

func (c *Client) NotifyBecomeMaster(engine *Engine) {
	c.listenerMu.RLock()
	handler := c.becomeMasterHandler
	c.listenerMu.RUnlock()

	if handler != nil {
		handler(engine)
	}
}

func (c *Client) SetGameOverHandler(handler func()) {
	c.listenerMu.Lock()
	c.gameOverHandler = handler
	c.listenerMu.Unlock()
}

func (c *Client) NotifyGameOver() {
	c.listenerMu.Lock()
	if c.gameOverNotified {
		c.listenerMu.Unlock()
		return
	}
	c.gameOverNotified = true
	handler := c.gameOverHandler
	c.listenerMu.Unlock()

	if handler != nil {
		handler()
	}
}

func (c *Client) SetGameConfig(config *protocol.GameConfig, gameName string) {
	c.gameConfig = config
	c.gameName = gameName
}

func (c *Client) GetGameConfig() *protocol.GameConfig {
	return c.gameConfig
}

func (c *Client) GetGameName() string {
	return c.gameName
}

func (c *Client) notifyStateListeners() {
	c.listenerMu.RLock()
	listeners := make([]func(*protocol.GameState), len(c.stateListeners))
	copy(listeners, c.stateListeners)
	c.listenerMu.RUnlock()

	state := c.GetState()
	for _, listener := range listeners {
		listener(state)
	}
}

func (c *Client) notifyGameListListeners() {
	c.listenerMu.RLock()
	listeners := make([]func(map[string]*GameInfo), len(c.gameListListeners))
	copy(listeners, c.gameListListeners)
	c.listenerMu.RUnlock()

	games := c.GetAvailableGames()
	for _, listener := range listeners {
		listener(games)
	}
}

func (c *Client) JoinGame(playerID int32, role protocol.NodeRole) {
	c.playerID = playerID
	c.role = role
	c.inGame = true
	c.gameActive = true
}

func (c *Client) GetDeputyID() int32 {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()

	if c.state == nil {
		return 0
	}

	for _, player := range c.state.Players {
		if player.Role == protocol.DEPUTY {
			return player.ID
		}
	}
	return 0
}

func (c *Client) PromoteNormalToDeputy(playerID int32) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	if c.state == nil {
		return
	}

	for i := range c.state.Players {
		if c.state.Players[i].ID == playerID {
			c.state.Players[i].Role = protocol.DEPUTY
			break
		}
	}

	if c.engine != nil {
		state := c.engine.GetState()
		for i := range state.Players {
			if state.Players[i].ID == playerID {
				state.Players[i].Role = protocol.DEPUTY
				break
			}
		}
	}
}
