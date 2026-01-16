package network

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"snakes/pkg/game"
	"snakes/pkg/protocol"
)

const (
	MulticastAddr = "239.192.0.4:9192"
	BroadcastAddr = "255.255.255.255:9192"
	MaxPacketSize = 65535
	GameInfoFile  = "snakes_game_info.json"
)

var gameInfoFileMu sync.Mutex

type Manager struct {
	client *game.Client

	unicastConn   *net.UDPConn
	multicastConn *net.UDPConn
	broadcastConn *net.UDPConn
	unicastPort   int

	msgSeq   int64
	msgSeqMu sync.Mutex

	pendingMessages map[int64]*PendingMessage
	pendingMu       sync.Mutex

	playerAddresses   map[int32]*net.UDPAddr
	lastPlayerContact map[int32]time.Time
	lastSentToPlayer  map[int32]time.Time
	lastMasterContact time.Time
	deputyID          int32
	addrMu            sync.RWMutex

	stopCh chan struct{}
	wg     sync.WaitGroup
}

type PendingMessage struct {
	Message    *Message
	Addr       *net.UDPAddr
	SentAt     time.Time
	RetryCount int
}

type Message struct {
	MsgSeq     int64  `json:"msg_seq"`
	SenderID   int32  `json:"sender_id,omitempty"`
	ReceiverID int32  `json:"receiver_id,omitempty"`
	Type       string `json:"type"`

	Ping         *PingMsg         `json:"ping,omitempty"`
	Steer        *SteerMsg        `json:"steer,omitempty"`
	Ack          *AckMsg          `json:"ack,omitempty"`
	State        *StateMsg        `json:"state,omitempty"`
	Announcement *AnnouncementMsg `json:"announcement,omitempty"`
	Join         *JoinMsg         `json:"join,omitempty"`
	Error        *ErrorMsg        `json:"error,omitempty"`
	Discover     *DiscoverMsg     `json:"discover,omitempty"`
	RoleChange   *RoleChangeMsg   `json:"role_change,omitempty"`
}

type RoleChangeMsg struct {
	SenderRole   protocol.NodeRole `json:"sender_role,omitempty"`
	ReceiverRole protocol.NodeRole `json:"receiver_role,omitempty"`
}

type PingMsg struct{}
type AckMsg struct{}
type DiscoverMsg struct{}

type SteerMsg struct {
	Direction protocol.Direction `json:"direction"`
}

type PlayerAddr struct {
	PlayerID int32  `json:"player_id"`
	IP       string `json:"ip"`
	Port     int32  `json:"port"`
}

type StateMsg struct {
	State           protocol.GameState `json:"state"`
	DeputyIP        string             `json:"deputy_ip,omitempty"`
	DeputyPort      int32              `json:"deputy_port,omitempty"`
	PlayerAddresses []PlayerAddr       `json:"player_addresses,omitempty"`
}

type AnnouncementMsg struct {
	Games      []protocol.GameAnnouncement `json:"games"`
	MasterPort int                         `json:"master_port"`
}

type JoinMsg struct {
	PlayerType    protocol.PlayerType `json:"player_type"`
	PlayerName    string              `json:"player_name"`
	GameName      string              `json:"game_name"`
	RequestedRole protocol.NodeRole   `json:"requested_role"`
}

type ErrorMsg struct {
	ErrorMessage string `json:"error_message"`
}

func NewManager(client *game.Client) *Manager {
	m := &Manager{
		client:            client,
		msgSeq:            1,
		pendingMessages:   make(map[int64]*PendingMessage),
		playerAddresses:   make(map[int32]*net.UDPAddr),
		lastPlayerContact: make(map[int32]time.Time),
		lastSentToPlayer:  make(map[int32]time.Time),
		stopCh:            make(chan struct{}),
	}

	client.SetLeaveHandler(func() {
		m.notifyDeputyBecomeMaster()
	})

	return m
}

func (m *Manager) notifyDeputyBecomeMaster() {
	if m.unicastConn == nil {
		return
	}

	deputyIP, deputyPort := m.client.GetDeputyAddress()
	if deputyIP == "" {
		return
	}

	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", deputyIP, deputyPort))
	if err != nil {
		return
	}

	m.addrMu.RLock()
	//собираем адреса всех игроков для передачи новому мастеру
	deputyID := m.deputyID
	var playerAddrs []PlayerAddr
	for playerID, playerAddr := range m.playerAddresses {
		if playerID != deputyID {
			playerAddrs = append(playerAddrs, PlayerAddr{
				PlayerID: playerID,
				IP:       playerAddr.IP.String(),
				Port:     int32(playerAddr.Port),
			})
		}
	}
	m.addrMu.RUnlock()

	if deputyID == 0 {
		state := m.client.GetState()
		if state != nil {
			for _, player := range state.Players {
				if player.Role == protocol.DEPUTY {
					deputyID = player.ID
					break
				}
			}
		}
	}

	if deputyID == 0 {
		return
	}

	state := m.client.GetState()
	if state != nil {
		stateMsg := &Message{
			MsgSeq:   m.getNextSeq(),
			SenderID: m.client.GetPlayerID(),
			Type:     "state",
			State: &StateMsg{
				State:           *state,
				PlayerAddresses: playerAddrs,
			},
		}
		stateData, _ := json.Marshal(stateMsg)
		m.unicastConn.WriteToUDP(stateData, addr)
		time.Sleep(10 * time.Millisecond)
	}

	roleMsg := &Message{
		MsgSeq:     m.getNextSeq(),
		SenderID:   m.client.GetPlayerID(),
		ReceiverID: deputyID,
		Type:       "role_change",
		RoleChange: &RoleChangeMsg{
			SenderRole:   protocol.VIEWER,
			ReceiverRole: protocol.MASTER,
		},
	}

	data, _ := json.Marshal(roleMsg)
	for i := 0; i < 5; i++ {
		if m.unicastConn == nil {
			return
		}
		m.unicastConn.WriteToUDP(data, addr)
		time.Sleep(20 * time.Millisecond)
	}
}

func (m *Manager) Start() error {

	unicastAddr, err := net.ResolveUDPAddr("udp", "0.0.0.0:0")
	if err != nil {
		return fmt.Errorf("failed to resolve unicast address: %w", err)
	}

	m.unicastConn, err = net.ListenUDP("udp", unicastAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on unicast socket: %w", err)
	}

	m.unicastPort = m.unicastConn.LocalAddr().(*net.UDPAddr).Port
	fmt.Printf("Listening on unicast port: %d\n", m.unicastPort)

	multicastAddr, err := net.ResolveUDPAddr("udp", MulticastAddr)
	if err != nil {
		m.unicastConn.Close()
		return fmt.Errorf("failed to resolve multicast address: %w", err)
	}

	interfaces, _ := net.Interfaces()
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 {
			continue
		}
		m.multicastConn, err = net.ListenMulticastUDP("udp", &iface, multicastAddr)
		if err == nil {
			fmt.Printf("Multicast on interface: %s\n", iface.Name)
			break
		}
	}

	if m.multicastConn == nil {
		m.multicastConn, err = net.ListenMulticastUDP("udp", nil, multicastAddr)
		if err != nil {
			fmt.Println("Warning: Multicast not available, using unicast only")
			m.multicastConn = nil
		}
	}

	broadcastListenAddr, err := net.ResolveUDPAddr("udp", "0.0.0.0:9192")
	if err == nil {
		m.broadcastConn, err = net.ListenUDP("udp", broadcastListenAddr)
		if err != nil {
			fmt.Println("Note: Broadcast port 9192 already in use (another instance running)")
			m.broadcastConn = nil
		} else {
			fmt.Println("Listening for broadcast on port 9192")
		}
	}

	m.wg.Add(6)
	go m.receiveUnicast()
	go m.receiveMulticast()
	go m.receiveBroadcast()
	go m.retryMessages()
	go m.sendAnnouncements()
	go m.checkTimeouts()

	return nil
}

func (m *Manager) Stop() {
	close(m.stopCh)
	m.wg.Wait()

	if m.unicastConn != nil {
		m.unicastConn.Close()
		m.unicastConn = nil
	}
	if m.multicastConn != nil {
		m.multicastConn.Close()
		m.multicastConn = nil
	}
	if m.broadcastConn != nil {
		m.broadcastConn.Close()
		m.broadcastConn = nil
	}

	m.addrMu.Lock()
	m.playerAddresses = make(map[int32]*net.UDPAddr)
	m.lastPlayerContact = make(map[int32]time.Time)
	m.lastSentToPlayer = make(map[int32]time.Time)
	m.lastMasterContact = time.Time{}
	m.addrMu.Unlock()

	m.pendingMu.Lock()
	m.pendingMessages = make(map[int64]*PendingMessage)
	m.pendingMu.Unlock()

	m.stopCh = make(chan struct{})
}

func (m *Manager) receiveUnicast() {
	defer m.wg.Done()

	buf := make([]byte, MaxPacketSize)

	for {
		select {
		case <-m.stopCh:
			return
		default:
		}

		m.unicastConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, addr, err := m.unicastConn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			continue
		}

		var msg Message
		if err := json.Unmarshal(buf[:n], &msg); err != nil {
			continue
		}

		m.handleMessage(&msg, addr)
	}
}

func (m *Manager) receiveMulticast() {
	defer m.wg.Done()

	if m.multicastConn == nil {
		return
	}

	buf := make([]byte, MaxPacketSize)

	for {
		select {
		case <-m.stopCh:
			return
		default:
		}

		m.multicastConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, addr, err := m.multicastConn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			continue
		}

		var msg Message
		if err := json.Unmarshal(buf[:n], &msg); err != nil {
			continue
		}

		m.handleMessage(&msg, addr)
	}
}

func (m *Manager) receiveBroadcast() {
	defer m.wg.Done()

	if m.broadcastConn == nil {
		return
	}

	buf := make([]byte, MaxPacketSize)

	for {
		select {
		case <-m.stopCh:
			return
		default:
		}

		m.broadcastConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, addr, err := m.broadcastConn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			continue
		}

		if addr.Port == m.unicastPort {
			continue
		}

		var msg Message
		if err := json.Unmarshal(buf[:n], &msg); err != nil {
			continue
		}

		m.handleMessage(&msg, addr)
	}
}

func (m *Manager) handleMessage(msg *Message, addr *net.UDPAddr) {
	if msg.SenderID != 0 {
		m.addrMu.Lock()
		m.playerAddresses[msg.SenderID] = addr
		m.lastPlayerContact[msg.SenderID] = time.Now()
		m.addrMu.Unlock()
	}

	switch msg.Type {
	case "ping":
		m.handlePing(msg, addr)
	case "steer":
		m.handleSteer(msg, addr)
	case "ack":
		m.handleAck(msg, addr)
	case "state":
		m.handleState(msg, addr)
	case "announcement":
		m.handleAnnouncement(msg, addr)
	case "join":
		m.handleJoin(msg, addr)
	case "error":
		m.handleError(msg, addr)
	case "discover":
		m.handleDiscover(msg, addr)
	case "role_change":
		m.handleRoleChange(msg, addr)
	}
}

func (m *Manager) handlePing(msg *Message, addr *net.UDPAddr) {
	m.sendAck(msg.MsgSeq, msg.SenderID, addr)

	if m.client.IsMaster() && msg.SenderID != 0 {
		m.addrMu.Lock()
		m.lastPlayerContact[msg.SenderID] = time.Now()
		m.playerAddresses[msg.SenderID] = addr
		m.addrMu.Unlock()
	}

	if !m.client.IsMaster() && m.client.IsInGame() {
		m.addrMu.Lock()
		m.lastMasterContact = time.Now()
		m.addrMu.Unlock()
	}
}

func (m *Manager) handleSteer(msg *Message, addr *net.UDPAddr) {
	m.sendAck(msg.MsgSeq, msg.SenderID, addr)

	if m.client.IsMaster() {
		m.addrMu.Lock()
		m.lastPlayerContact[msg.SenderID] = time.Now()
		m.playerAddresses[msg.SenderID] = addr
		m.addrMu.Unlock()

		if msg.Steer != nil {
			engine := m.client.GetEngine()
			if engine != nil {
				engine.RequestSteer(msg.SenderID, msg.Steer.Direction, msg.MsgSeq)
			}
		}
	}
}

func (m *Manager) handleAck(msg *Message, addr *net.UDPAddr) {
	m.pendingMu.Lock()
	delete(m.pendingMessages, msg.MsgSeq)
	m.pendingMu.Unlock()

	m.addrMu.Lock()
	if m.client.IsMaster() && msg.SenderID != 0 {
		m.lastPlayerContact[msg.SenderID] = time.Now()
		m.playerAddresses[msg.SenderID] = addr
	} else if !m.client.IsMaster() && m.client.IsInGame() {
		m.lastMasterContact = time.Now()
	}
	m.addrMu.Unlock()

	if msg.ReceiverID != 0 && m.client.GetPlayerID() == 0 {
		m.client.JoinGame(msg.ReceiverID, protocol.NORMAL)

		m.addrMu.Lock()
		m.lastMasterContact = time.Now()
		m.addrMu.Unlock()
	}
}

func (m *Manager) handleState(msg *Message, addr *net.UDPAddr) {
	m.sendAck(msg.MsgSeq, msg.SenderID, addr)

	if m.client.IsMaster() {
		return
	}

	m.addrMu.Lock()
	m.lastMasterContact = time.Now()
	m.addrMu.Unlock()

	if msg.State != nil {
		m.client.UpdateState(&msg.State.State)

		m.client.SetMasterAddress(addr.IP.String(), int32(addr.Port))

		if msg.State.DeputyIP != "" && msg.State.DeputyPort != 0 {
			m.client.SetDeputyAddress(msg.State.DeputyIP, msg.State.DeputyPort)
		}

		if len(msg.State.PlayerAddresses) > 0 {
			m.addrMu.Lock()
			for _, pa := range msg.State.PlayerAddresses {
				playerAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", pa.IP, pa.Port))
				if err == nil {
					m.playerAddresses[pa.PlayerID] = playerAddr
					m.lastPlayerContact[pa.PlayerID] = time.Now()
				}
			}
			m.addrMu.Unlock()
		}
	}
}

func (m *Manager) handleAnnouncement(msg *Message, addr *net.UDPAddr) {
	if msg.Announcement == nil {
		return
	}

	masterPort := int32(msg.Announcement.MasterPort)
	if masterPort == 0 {
		masterPort = int32(addr.Port)
	}

	for _, gameAnn := range msg.Announcement.Games {
		info := &game.GameInfo{
			GameName:   gameAnn.GameName,
			Players:    gameAnn.Players,
			Config:     gameAnn.Config,
			CanJoin:    gameAnn.CanJoin,
			MasterIP:   addr.IP.String(),
			MasterPort: masterPort,
			LastSeen:   time.Now(),
		}
		m.client.UpdateAvailableGames(info)
	}
}

func (m *Manager) handleJoin(msg *Message, addr *net.UDPAddr) {
	if !m.client.IsMaster() {
		return
	}

	if msg.Join == nil {
		return
	}

	engine := m.client.GetEngine()
	if engine == nil {
		return
	}

	if !engine.CanJoin() && msg.Join.RequestedRole != protocol.VIEWER {
		m.sendError("No free space on the field", addr)
		return
	}

	requestedRole := msg.Join.RequestedRole
	if requestedRole == protocol.VIEWER {
	} else {
		requestedRole = protocol.NORMAL
	}

	player, err := engine.AddPlayer(msg.Join.PlayerName, requestedRole, msg.Join.PlayerType)
	if err != nil {
		m.sendError(err.Error(), addr)
		return
	}

	m.addrMu.Lock()
	m.playerAddresses[player.ID] = addr
	m.lastPlayerContact[player.ID] = time.Now().Add(time.Second)
	m.addrMu.Unlock()

	ackMsg := &Message{
		MsgSeq:     msg.MsgSeq,
		SenderID:   m.client.GetPlayerID(),
		ReceiverID: player.ID,
		Type:       "ack",
		Ack:        &AckMsg{},
	}
	data, _ := json.Marshal(ackMsg)
	m.unicastConn.WriteToUDP(data, addr)

	if player.Role == protocol.NORMAL {
		deputyIP, _ := m.client.GetDeputyAddress()
		if deputyIP == "" {
			deputyAddr := addr.IP.String()
			deputyPort := int32(addr.Port)
			m.client.SetDeputyAddress(deputyAddr, deputyPort)

			m.addrMu.Lock()
			m.deputyID = player.ID
			m.addrMu.Unlock()

			state := engine.GetState()
			for i := range state.Players {
				if state.Players[i].ID == player.ID {
					state.Players[i].Role = protocol.DEPUTY
					break
				}
			}

			m.SendRoleChange(player.ID, protocol.MASTER, protocol.DEPUTY)
		}
	}

	m.sendStateToPlayer(player.ID)
}

func (m *Manager) handleError(msg *Message, addr *net.UDPAddr) {
	if msg.Error != nil {
		if msg.Error.ErrorMessage == "GAME_OVER" {
			m.client.NotifyGameOver()
		} else {
			m.client.NotifyError(msg.Error.ErrorMessage)
		}
	}
}

func (m *Manager) handleDiscover(msg *Message, addr *net.UDPAddr) {
	if m.client.IsMaster() {
		m.sendAnnouncementTo(addr)
	}
}

func (m *Manager) handleRoleChange(msg *Message, addr *net.UDPAddr) {
	m.sendAck(msg.MsgSeq, msg.SenderID, addr)

	if msg.RoleChange == nil {
		return
	}

	if msg.RoleChange.ReceiverRole == protocol.DEPUTY {
		m.client.SetRole(protocol.DEPUTY)
		m.addrMu.Lock()
		m.lastMasterContact = time.Now()
		m.addrMu.Unlock()
	} else if msg.RoleChange.ReceiverRole == protocol.MASTER {

		m.becomeMaster()
	} else if msg.RoleChange.ReceiverRole == protocol.VIEWER {

		if m.client.IsMaster() {
			return
		}

		m.client.NotifyError("Ваша змейка погибла! Вы теперь наблюдатель.")
		m.client.SetRole(protocol.VIEWER)

		m.addrMu.Lock()
		m.lastMasterContact = time.Now()
		m.addrMu.Unlock()
	}

	if msg.RoleChange.SenderRole == protocol.MASTER {
		m.client.SetMasterAddress(addr.IP.String(), int32(addr.Port))
		m.addrMu.Lock()
		m.lastMasterContact = time.Now()
		m.addrMu.Unlock()
	}

	if msg.RoleChange.SenderRole == protocol.VIEWER && m.client.IsMaster() {
		engine := m.client.GetEngine()
		if engine != nil {
			engine.MakePlayerZombie(msg.SenderID)

			if msg.SenderID == m.deputyID {
				m.addrMu.Lock()
				m.deputyID = 0
				m.client.SetDeputyAddress("", 0)
				m.addrMu.Unlock()
			}
		}
	}
}

func (m *Manager) becomeMaster() {
	if m.client.IsMaster() {
		return
	}

	time.Sleep(50 * time.Millisecond)

	state := m.client.GetState()
	if state == nil {
		return
	}

	config := m.client.GetGameConfig()
	gameName := m.client.GetGameName()

	if config == nil {
		config = &protocol.GameConfig{
			Width:        40,
			Height:       30,
			FoodStatic:   1,
			StateDelayMs: 300,
		}
		gameName = "Restored Game"
	}

	m.client.SetRole(protocol.MASTER)
	m.client.SetGameActive(true)

	engine := game.NewEngineFromState(config, gameName, state)
	m.client.SetEngine(engine)

	engineState := engine.GetState()
	myID := m.client.GetPlayerID()
	for i := range engineState.Players {
		if engineState.Players[i].ID == myID {
			engineState.Players[i].Role = protocol.MASTER
		} else if engineState.Players[i].Role == protocol.MASTER {
			engineState.Players[i].Role = protocol.VIEWER
		} else if engineState.Players[i].Role == protocol.DEPUTY {

			hasAliveSnake := false
			for _, snake := range engineState.Snakes {
				if snake.PlayerID == engineState.Players[i].ID && snake.State == protocol.ALIVE {
					hasAliveSnake = true
					break
				}
			}
			if !hasAliveSnake {
				engineState.Players[i].Role = protocol.VIEWER
			}
		}
	}

	m.client.UpdateState(engineState)

	m.assignNewDeputy()

	m.broadcastRoleChange(protocol.MASTER, 0)

	m.client.NotifyBecomeMaster(engine)
}

func (m *Manager) broadcastRoleChange(senderRole protocol.NodeRole, receiverRole protocol.NodeRole) {
	state := m.client.GetState()
	if state == nil {
		return
	}

	m.addrMu.RLock()
	defer m.addrMu.RUnlock()

	for _, player := range state.Players {
		if player.ID == m.client.GetPlayerID() {
			continue
		}

		addr, exists := m.playerAddresses[player.ID]
		if !exists {
			continue
		}

		roleMsg := &Message{
			MsgSeq:     m.getNextSeq(),
			SenderID:   m.client.GetPlayerID(),
			ReceiverID: player.ID,
			Type:       "role_change",
			RoleChange: &RoleChangeMsg{
				SenderRole:   senderRole,
				ReceiverRole: receiverRole,
			},
		}

		m.sendMessageWithRetry(roleMsg, addr)
	}
}

func (m *Manager) SendRoleChange(playerID int32, senderRole, receiverRole protocol.NodeRole) {
	m.addrMu.RLock()
	addr, exists := m.playerAddresses[playerID]
	m.addrMu.RUnlock()

	if !exists {
		return
	}

	roleMsg := &Message{
		MsgSeq:     m.getNextSeq(),
		SenderID:   m.client.GetPlayerID(),
		ReceiverID: playerID,
		Type:       "role_change",
		RoleChange: &RoleChangeMsg{
			SenderRole:   senderRole,
			ReceiverRole: receiverRole,
		},
	}

	m.sendMessageWithRetry(roleMsg, addr)
}

func (m *Manager) SendMasterRoleWithState(playerID int32) {
	m.addrMu.RLock()
	addr, exists := m.playerAddresses[playerID]

	var playerAddrs []PlayerAddr
	for pid, paddr := range m.playerAddresses {
		if pid != playerID {
			playerAddrs = append(playerAddrs, PlayerAddr{
				PlayerID: pid,
				IP:       paddr.IP.String(),
				Port:     int32(paddr.Port),
			})
		}
	}
	m.addrMu.RUnlock()

	if !exists {
		return
	}

	state := m.client.GetState()
	if state != nil {
		stateMsg := &Message{
			MsgSeq:   m.getNextSeq(),
			SenderID: m.client.GetPlayerID(),
			Type:     "state",
			State: &StateMsg{
				State:           *state,
				PlayerAddresses: playerAddrs,
			},
		}
		data, _ := json.Marshal(stateMsg)
		m.unicastConn.WriteToUDP(data, addr)
		time.Sleep(10 * time.Millisecond)
	}

	roleMsg := &Message{
		MsgSeq:     m.getNextSeq(),
		SenderID:   m.client.GetPlayerID(),
		ReceiverID: playerID,
		Type:       "role_change",
		RoleChange: &RoleChangeMsg{
			SenderRole:   protocol.MASTER,
			ReceiverRole: protocol.MASTER,
		},
	}

	m.sendMessageWithRetry(roleMsg, addr)
}

func (m *Manager) TransferMasterRole() {
	if !m.client.IsMaster() {
		return
	}

	m.notifyDeputyBecomeMaster()
}

func (m *Manager) SetDeputyInfo(playerID int32, ip string, port int32) {
	m.addrMu.Lock()
	m.deputyID = playerID
	m.addrMu.Unlock()

	m.client.SetDeputyAddress(ip, port)
}

func (m *Manager) RequestViewerMode() {
	if m.client.IsMaster() {
		m.notifyDeputyBecomeMaster()

		engine := m.client.GetEngine()
		if engine != nil {
			engine.MakePlayerZombie(m.client.GetPlayerID())
		}

		m.client.SetRole(protocol.VIEWER)
		return
	}

	masterIP, masterPort := m.client.GetMasterAddress()
	if masterIP == "" {
		return
	}

	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", masterIP, masterPort))
	if err != nil {
		return
	}

	roleMsg := &Message{
		MsgSeq:   m.getNextSeq(),
		SenderID: m.client.GetPlayerID(),
		Type:     "role_change",
		RoleChange: &RoleChangeMsg{
			SenderRole: protocol.VIEWER,
		},
	}

	m.sendMessageWithRetry(roleMsg, addr)
}

func (m *Manager) sendAck(msgSeq int64, receiverID int32, addr *net.UDPAddr) {
	ackMsg := &Message{
		MsgSeq:     m.getNextSeq(),
		SenderID:   m.client.GetPlayerID(),
		ReceiverID: receiverID,
		Type:       "ack",
		Ack:        &AckMsg{},
	}

	data, _ := json.Marshal(ackMsg)
	m.unicastConn.WriteToUDP(data, addr)
}

func (m *Manager) sendError(message string, addr *net.UDPAddr) {
	errorMsg := &Message{
		MsgSeq: m.getNextSeq(),
		Type:   "error",
		Error:  &ErrorMsg{ErrorMessage: message},
	}

	m.sendMessage(errorMsg, addr)
}

func (m *Manager) SendSteer(direction protocol.Direction) {
	if m.client.IsMaster() {
		engine := m.client.GetEngine()
		if engine != nil {
			// Для локального MASTER используем свой msg_seq
			engine.RequestSteer(m.client.GetPlayerID(), direction, m.getNextSeq())
		}
		return
	}

	masterIP, masterPort := m.client.GetMasterAddress()
	if masterIP == "" {
		return
	}

	playerID := m.client.GetPlayerID()
	if playerID == 0 {
		return
	}

	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", masterIP, masterPort))
	if err != nil {
		return
	}

	steerMsg := &Message{
		MsgSeq:   m.getNextSeq(),
		SenderID: playerID,
		Type:     "steer",
		Steer:    &SteerMsg{Direction: direction},
	}

	m.sendMessage(steerMsg, addr)
}

func (m *Manager) SendJoin(gameName string, asViewer bool) {
	masterIP, masterPort := m.client.GetMasterAddress()
	if masterIP == "" {
		return
	}

	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", masterIP, masterPort))
	if err != nil {
		return
	}

	role := protocol.NORMAL
	if asViewer {
		role = protocol.VIEWER
	}

	joinMsg := &Message{
		MsgSeq: m.getNextSeq(),
		Type:   "join",
		Join: &JoinMsg{
			PlayerType:    protocol.HUMAN,
			PlayerName:    m.client.GetPlayerName(),
			GameName:      gameName,
			RequestedRole: role,
		},
	}

	m.sendMessageWithRetry(joinMsg, addr)
}

func (m *Manager) sendAnnouncements() {
	defer m.wg.Done()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			if m.client.IsMaster() && m.client.IsGameActive() {
				m.sendAnnouncementMulticast()
			}
			m.readGameInfoFromFile()
			m.client.CleanupOldGames(5 * time.Second)
		}
	}
}

func (m *Manager) sendAnnouncementMulticast() {
	engine := m.client.GetEngine()
	if engine == nil {
		return
	}

	if !m.client.IsGameActive() {
		return
	}

	announcement := m.createAnnouncement(engine)

	announcementMsg := &Message{
		MsgSeq:   m.getNextSeq(),
		SenderID: m.client.GetPlayerID(),
		Type:     "announcement",
		Announcement: &AnnouncementMsg{
			Games:      []protocol.GameAnnouncement{announcement},
			MasterPort: m.unicastPort,
		},
	}

	data, err := json.Marshal(announcementMsg)
	if err != nil {
		return
	}

	multicastAddr, _ := net.ResolveUDPAddr("udp", MulticastAddr)
	m.unicastConn.WriteToUDP(data, multicastAddr)

	broadcastAddr, _ := net.ResolveUDPAddr("udp", BroadcastAddr)
	m.unicastConn.WriteToUDP(data, broadcastAddr)

	m.sendToAllSubnets(data)

	m.writeGameInfoFile(data)
}

func (m *Manager) sendToAllSubnets(data []byte) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return
	}

	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}

			ip := ipNet.IP.To4()
			if ip == nil {
				continue
			}

			broadcast := make(net.IP, 4)
			for i := 0; i < 4; i++ {
				broadcast[i] = ip[i] | ^ipNet.Mask[i]
			}

			broadcastUDP, _ := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:9192", broadcast.String()))
			m.unicastConn.WriteToUDP(data, broadcastUDP)
		}
	}
}

func (m *Manager) writeGameInfoFile(data []byte) {
	gameInfoFileMu.Lock()
	defer gameInfoFileMu.Unlock()

	tempDir := os.TempDir()
	filePath := filepath.Join(tempDir, GameInfoFile)

	type gameEntry struct {
		Port      int             `json:"port"`
		Data      json.RawMessage `json:"data"`
		Timestamp int64           `json:"timestamp"`
	}

	existingData, _ := os.ReadFile(filePath)
	var existingGames []gameEntry
	json.Unmarshal(existingData, &existingGames)

	now := time.Now().Unix()
	var filteredGames []gameEntry
	for _, g := range existingGames {
		if now-g.Timestamp <= 10 && g.Port != m.unicastPort {
			filteredGames = append(filteredGames, g)
		}
	}

	newEntry := gameEntry{
		Port:      m.unicastPort,
		Data:      data,
		Timestamp: now,
	}
	filteredGames = append(filteredGames, newEntry)

	newData, _ := json.Marshal(filteredGames)
	os.WriteFile(filePath, newData, 0644)
}

func (m *Manager) readGameInfoFromFile() {
	gameInfoFileMu.Lock()
	defer gameInfoFileMu.Unlock()

	tempDir := os.TempDir()
	filePath := filepath.Join(tempDir, GameInfoFile)

	data, err := os.ReadFile(filePath)
	if err != nil {
		return
	}

	var games []struct {
		Port      int             `json:"port"`
		Data      json.RawMessage `json:"data"`
		Timestamp int64           `json:"timestamp"`
	}

	if err := json.Unmarshal(data, &games); err != nil {
		return
	}

	now := time.Now().Unix()
	for _, g := range games {
		if now-g.Timestamp > 10 {
			continue
		}

		if g.Port == m.unicastPort {
			continue
		}

		var msg Message
		if err := json.Unmarshal(g.Data, &msg); err != nil {
			continue
		}

		addr, _ := net.ResolveUDPAddr("udp", fmt.Sprintf("127.0.0.1:%d", g.Port))
		m.handleMessage(&msg, addr)
	}
}

func (m *Manager) sendAnnouncementTo(addr *net.UDPAddr) {
	engine := m.client.GetEngine()
	if engine == nil {
		return
	}

	announcement := m.createAnnouncement(engine)

	announcementMsg := &Message{
		MsgSeq:   m.getNextSeq(),
		SenderID: m.client.GetPlayerID(),
		Type:     "announcement",
		Announcement: &AnnouncementMsg{
			Games:      []protocol.GameAnnouncement{announcement},
			MasterPort: m.unicastPort,
		},
	}

	data, _ := json.Marshal(announcementMsg)
	m.unicastConn.WriteToUDP(data, addr)
}

func (m *Manager) createAnnouncement(engine *game.Engine) protocol.GameAnnouncement {
	state := engine.GetState()
	config := engine.GetConfig()

	activePlayers := make([]protocol.GamePlayer, 0)
	for _, player := range state.Players {
		if player.Role != protocol.VIEWER {
			activePlayers = append(activePlayers, player)
		}
	}

	return protocol.GameAnnouncement{
		Players:  activePlayers,
		Config:   *config,
		CanJoin:  engine.CanJoin(),
		GameName: engine.GetGameName(),
	}
}

func (m *Manager) sendStateToPlayer(playerID int32) {
	m.addrMu.RLock()
	addr, exists := m.playerAddresses[playerID]
	m.addrMu.RUnlock()

	if !exists {
		return
	}

	engine := m.client.GetEngine()
	if engine == nil {
		return
	}

	deputyIP, deputyPort := m.client.GetDeputyAddress()

	stateMsg := &Message{
		MsgSeq:   m.getNextSeq(),
		SenderID: m.client.GetPlayerID(),
		Type:     "state",
		State: &StateMsg{
			State:      *engine.GetState(),
			DeputyIP:   deputyIP,
			DeputyPort: deputyPort,
		},
	}

	m.sendMessageWithRetry(stateMsg, addr)
}

func (m *Manager) BroadcastState() {
	if !m.client.IsMaster() {
		return
	}

	engine := m.client.GetEngine()
	if engine == nil {
		return
	}

	state := engine.GetState()

	deputyIP, deputyPort := m.client.GetDeputyAddress()

	m.addrMu.RLock()
	defer m.addrMu.RUnlock()

	stateData := &StateMsg{
		State:      *state,
		DeputyIP:   deputyIP,
		DeputyPort: deputyPort,
	}

	for _, player := range state.Players {
		if player.ID == m.client.GetPlayerID() {
			continue
		}

		addr, exists := m.playerAddresses[player.ID]
		if !exists {
			continue
		}

		stateMsg := &Message{
			MsgSeq:   m.getNextSeq(),
			SenderID: m.client.GetPlayerID(),
			Type:     "state",
			State:    stateData,
		}

		m.sendMessage(stateMsg, addr)
	}
}

func (m *Manager) BroadcastGameOver() {
	if !m.client.IsMaster() {
		return
	}

	state := m.client.GetState()
	if state == nil {
		return
	}

	m.addrMu.RLock()
	defer m.addrMu.RUnlock()

	for _, player := range state.Players {
		if player.ID == m.client.GetPlayerID() {
			continue
		}

		addr, exists := m.playerAddresses[player.ID]
		if !exists {
			continue
		}

		gameOverMsg := &Message{
			MsgSeq:   m.getNextSeq(),
			SenderID: m.client.GetPlayerID(),
			Type:     "error",
			Error:    &ErrorMsg{ErrorMessage: "GAME_OVER"},
		}

		data, _ := json.Marshal(gameOverMsg)
		for i := 0; i < 3; i++ {
			m.unicastConn.WriteToUDP(data, addr)
		}
	}
}

func (m *Manager) sendMessage(msg *Message, addr *net.UDPAddr) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	m.unicastConn.WriteToUDP(data, addr)
}

func (m *Manager) sendMessageWithRetry(msg *Message, addr *net.UDPAddr) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	m.unicastConn.WriteToUDP(data, addr)

	m.pendingMu.Lock()
	m.pendingMessages[msg.MsgSeq] = &PendingMessage{
		Message:    msg,
		Addr:       addr,
		SentAt:     time.Now(),
		RetryCount: 0,
	}
	m.pendingMu.Unlock()
}

func (m *Manager) retryMessages() {
	defer m.wg.Done()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.pendingMu.Lock()
			now := time.Now()

			for seq, pending := range m.pendingMessages {
				if now.Sub(pending.SentAt) > 100*time.Millisecond {
					data, _ := json.Marshal(pending.Message)
					m.unicastConn.WriteToUDP(data, pending.Addr)

					pending.SentAt = now
					pending.RetryCount++

					if pending.RetryCount > 10 {
						delete(m.pendingMessages, seq)
					}
				}
			}

			m.pendingMu.Unlock()
		}
	}
}

func (m *Manager) getNextSeq() int64 {
	m.msgSeqMu.Lock()
	defer m.msgSeqMu.Unlock()

	seq := m.msgSeq
	m.msgSeq++
	return seq
}

func (m *Manager) checkTimeouts() {
	defer m.wg.Done()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.doTimeoutCheck()
		}
	}
}

func (m *Manager) doTimeoutCheck() {
	if !m.client.IsInGame() {
		return
	}

	stateDelay := int32(300)
	engine := m.client.GetEngine()
	if engine != nil {
		stateDelay = engine.GetConfig().StateDelayMs
	} else if config := m.client.GetGameConfig(); config != nil {
		stateDelay = config.StateDelayMs
	}

	pingInterval := time.Duration(stateDelay/10) * time.Millisecond

	timeoutThreshold := time.Duration(stateDelay*2) * time.Millisecond

	now := time.Now()

	m.addrMu.Lock()
	defer m.addrMu.Unlock()

	if m.client.IsMaster() {
		for playerID, addr := range m.playerAddresses {
			if playerID == m.client.GetPlayerID() {
				continue
			}

			lastSent, exists := m.lastSentToPlayer[playerID]
			if !exists || now.Sub(lastSent) > pingInterval {
				m.sendPingUnlocked(playerID, addr)
			}
		}

		for playerID, lastContact := range m.lastPlayerContact {
			if playerID == m.client.GetPlayerID() {
				continue
			}

			playerRole := protocol.NORMAL
			if engine != nil {
				state := engine.GetState()
				for _, player := range state.Players {
					if player.ID == playerID {
						playerRole = player.Role
						break
					}
				}
			}

			if playerRole == protocol.VIEWER {
				continue
			}

			timeSinceContact := now.Sub(lastContact)
			if timeSinceContact > timeoutThreshold {
				if engine != nil {
					engine.MakePlayerZombie(playerID)
				}

				wasDeputy := (playerID == m.deputyID)

				state := engine.GetState()
				for i, player := range state.Players {
					if player.ID == playerID {
						state.Players[i].Role = protocol.VIEWER
						break
					}
				}

				delete(m.lastPlayerContact, playerID)

				if wasDeputy {
					m.deputyID = 0 // Сбрасываем ID DEPUTY
					m.assignNewDeputyUnlocked(engine)
				}
			}
		}

		m.ensureDeputyExistsUnlocked(engine)
	}

	if !m.client.IsMaster() && m.client.IsInGame() {
		if m.client.IsViewer() {
			return
		}

		if !m.lastMasterContact.IsZero() && now.Sub(m.lastMasterContact) > timeoutThreshold {
			if m.client.IsDeputy() {
				m.becomeMasterUnlocked()
			} else {
				deputyIP, deputyPort := m.client.GetDeputyAddress()
				if deputyIP != "" {
					m.client.SetMasterAddress(deputyIP, deputyPort)
					m.lastMasterContact = time.Now()

					addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", deputyIP, deputyPort))
					if err == nil {
						pingMsg := &Message{
							MsgSeq:   m.getNextSeq(),
							SenderID: m.client.GetPlayerID(),
							Type:     "ping",
							Ping:     &PingMsg{},
						}
						data, _ := json.Marshal(pingMsg)
						m.unicastConn.WriteToUDP(data, addr)
					}
				}
			}
		}
	}
}

func (m *Manager) becomeMasterUnlocked() {
	state := m.client.GetState()
	if state == nil {
		return
	}

	config := m.client.GetGameConfig()
	gameName := m.client.GetGameName()

	if config == nil {
		config = &protocol.GameConfig{
			Width:        40,
			Height:       30,
			FoodStatic:   1,
			StateDelayMs: 300,
		}
		gameName = "Restored Game"
	}

	engine := game.NewEngineFromState(config, gameName, state)

	myID := m.client.GetPlayerID()
	engineState := engine.GetState()
	for i := range engineState.Players {
		if engineState.Players[i].ID == myID {
			engineState.Players[i].Role = protocol.MASTER
		} else if engineState.Players[i].Role == protocol.MASTER {
			engineState.Players[i].Role = protocol.VIEWER
		}
	}

	m.client.SetEngine(engine)
	m.client.SetRole(protocol.MASTER)
	m.client.SetGameActive(true)

	var newDeputyID int32
	var newDeputyAddr *net.UDPAddr
	for _, player := range engineState.Players {
		if player.Role == protocol.NORMAL && player.ID != myID {
			if addr, exists := m.playerAddresses[player.ID]; exists {
				newDeputyID = player.ID
				newDeputyAddr = addr
				break
			}
		}
	}

	for _, player := range engineState.Players {
		if player.ID == myID {
			continue
		}

		addr, exists := m.playerAddresses[player.ID]
		if !exists {
			continue
		}

		receiverRole := protocol.NodeRole(0)
		if player.ID == newDeputyID {
			receiverRole = protocol.DEPUTY
			for i := range engineState.Players {
				if engineState.Players[i].ID == newDeputyID {
					engineState.Players[i].Role = protocol.DEPUTY
					break
				}
			}
		}

		roleMsg := &Message{
			MsgSeq:     m.getNextSeq(),
			SenderID:   myID,
			ReceiverID: player.ID,
			Type:       "role_change",
			RoleChange: &RoleChangeMsg{
				SenderRole:   protocol.MASTER,
				ReceiverRole: receiverRole,
			},
		}

		data, _ := json.Marshal(roleMsg)
		m.unicastConn.WriteToUDP(data, addr)
	}

	if newDeputyAddr != nil {
		m.client.SetDeputyAddress(newDeputyAddr.IP.String(), int32(newDeputyAddr.Port))
		m.deputyID = newDeputyID
	} else {
		m.client.SetDeputyAddress("", 0)
		m.deputyID = 0
	}

	m.client.NotifyBecomeMaster(engine)
}

func (m *Manager) assignNewDeputy() {
	engine := m.client.GetEngine()
	if engine == nil {
		return
	}

	m.addrMu.Lock()
	defer m.addrMu.Unlock()

	m.assignNewDeputyUnlocked(engine)
}

func (m *Manager) assignNewDeputyUnlocked(engine *game.Engine) {
	if engine == nil {
		return
	}

	state := engine.GetState()
	myID := m.client.GetPlayerID()

	var newDeputyID int32
	var newDeputyAddr *net.UDPAddr

	for _, player := range state.Players {
		if player.ID == myID {
			continue
		}
		if player.Role == protocol.NORMAL {
			if addr, exists := m.playerAddresses[player.ID]; exists {
				newDeputyID = player.ID
				newDeputyAddr = addr
				break
			}
		}
	}

	if newDeputyID == 0 {
		m.client.SetDeputyAddress("", 0)
		return
	}

	for i := range state.Players {
		if state.Players[i].ID == newDeputyID {
			state.Players[i].Role = protocol.DEPUTY
			break
		}
	}

	m.deputyID = newDeputyID

	m.client.SetDeputyAddress(newDeputyAddr.IP.String(), int32(newDeputyAddr.Port))

	roleMsg := &Message{
		MsgSeq:     m.getNextSeq(),
		SenderID:   myID,
		ReceiverID: newDeputyID,
		Type:       "role_change",
		RoleChange: &RoleChangeMsg{
			SenderRole:   protocol.MASTER,
			ReceiverRole: protocol.DEPUTY,
		},
	}

	data, _ := json.Marshal(roleMsg)
	m.unicastConn.WriteToUDP(data, newDeputyAddr)
}

func (m *Manager) ensureDeputyExistsUnlocked(engine *game.Engine) {
	if engine == nil {
		return
	}

	deputyIP, _ := m.client.GetDeputyAddress()
	if deputyIP != "" && m.deputyID != 0 {
		state := engine.GetState()
		deputyExists := false
		for _, player := range state.Players {
			if player.ID == m.deputyID && player.Role == protocol.DEPUTY {
				deputyExists = true
				break
			}
		}
		if deputyExists {
			return
		}
	}

	m.deputyID = 0
	m.client.SetDeputyAddress("", 0)
	m.assignNewDeputyUnlocked(engine)
}

func (m *Manager) sendPingUnlocked(playerID int32, addr *net.UDPAddr) {
	pingMsg := &Message{
		MsgSeq:   m.getNextSeq(),
		SenderID: m.client.GetPlayerID(),
		Type:     "ping",
		Ping:     &PingMsg{},
	}

	data, _ := json.Marshal(pingMsg)
	m.unicastConn.WriteToUDP(data, addr)
	m.lastSentToPlayer[playerID] = time.Now()
}

func (m *Manager) GetPlayerAddress(playerID int32) (string, int32) {
	m.addrMu.RLock()
	defer m.addrMu.RUnlock()

	addr, exists := m.playerAddresses[playerID]
	if !exists || addr == nil {
		return "", 0
	}

	return addr.IP.String(), int32(addr.Port)
}
