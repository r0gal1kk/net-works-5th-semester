package ui

import (
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"snakes/pkg/game"
	"snakes/pkg/network"
	"snakes/pkg/protocol"
)

type GUI struct {
	app     fyne.App
	window  fyne.Window
	client  *game.Client
	network *network.Manager

	currentScreen string
	gameRenderer  *GameRenderer
	playersList   *widget.List
	roleLabel     *widget.Label

	deathDialogShown bool
	joinErrorShown   bool
	lastSteerTime    time.Time
}

func NewGUI(client *game.Client, netManager *network.Manager) *GUI {
	myApp := app.New()

	window := myApp.NewWindow("Snake Multiplayer Game")
	window.Resize(fyne.NewSize(1000, 700))
	window.CenterOnScreen()

	gui := &GUI{
		app:           myApp,
		window:        window,
		client:        client,
		network:       netManager,
		currentScreen: "menu",
	}

	client.AddStateListener(func(state *protocol.GameState) {
		if gui.gameRenderer == nil {
			return
		}
		fyne.Do(func() {
			if gui.gameRenderer != nil {
				gui.gameRenderer.Refresh()
			}
			if gui.playersList != nil {
				gui.playersList.Refresh()
			}
			if gui.roleLabel != nil {
				gui.roleLabel.SetText(fmt.Sprintf("Ваша роль: %s", roleToString(gui.client.GetRole())))
			}
		})
	})

	client.AddGameListListener(func(games map[string]*game.GameInfo) {
		fyne.Do(func() {
			if gui.currentScreen == "menu" {
				gui.showMenuScreen()
			}
		})
	})

	client.SetErrorHandler(func(message string) {
		fyne.Do(func() {
			if message == "No free space on the field" {
				gui.joinErrorShown = true
				dialog.ShowError(fmt.Errorf("На поле нет свободного места для размещения змейки"), gui.window)
				return
			}

			if gui.client.GetRole() == protocol.VIEWER {
				return
			}
			if gui.deathDialogShown {
				return
			}
			gui.deathDialogShown = true
			dialog.ShowInformation("Уведомление", message, gui.window)
		})
	})

	client.SetBecomeMasterHandler(func(engine *game.Engine) {
		fyne.Do(func() {

			gui.startGameLoopWithEngine(engine)
		})
	})

	client.SetGameOverHandler(func() {
		fyne.Do(func() {
			dialog.ShowInformation("Игра завершена",
				"Последний игрок покинул игру. Игра завершена.", gui.window)
			gui.leaveGame()
		})
	})

	minSteerInterval := 50 * time.Millisecond

	window.Canvas().SetOnTypedRune(func(r rune) {
		if gui.currentScreen == "game" {
			now := time.Now()
			if now.Sub(gui.lastSteerTime) < minSteerInterval {
				return
			}

			switch r {
			case 'w', 'W':
				gui.lastSteerTime = now
				netManager.SendSteer(protocol.UP)
			case 'a', 'A':
				gui.lastSteerTime = now
				netManager.SendSteer(protocol.LEFT)
			case 's', 'S':
				gui.lastSteerTime = now
				netManager.SendSteer(protocol.DOWN)
			case 'd', 'D':
				gui.lastSteerTime = now
				netManager.SendSteer(protocol.RIGHT)
			case 'q', 'Q':
				gui.leaveGame()
			}
		}
	})

	window.Canvas().SetOnTypedKey(func(event *fyne.KeyEvent) {
		if gui.currentScreen == "game" {
			now := time.Now()
			if now.Sub(gui.lastSteerTime) < minSteerInterval {
				if event.Name != fyne.KeyEscape {
					return
				}
			}

			switch event.Name {
			case fyne.KeyUp:
				gui.lastSteerTime = now
				netManager.SendSteer(protocol.UP)
			case fyne.KeyLeft:
				gui.lastSteerTime = now
				netManager.SendSteer(protocol.LEFT)
			case fyne.KeyDown:
				gui.lastSteerTime = now
				netManager.SendSteer(protocol.DOWN)
			case fyne.KeyRight:
				gui.lastSteerTime = now
				netManager.SendSteer(protocol.RIGHT)
			case fyne.KeyEscape:
				gui.becomeViewer()
			}
		}
	})

	if client.GetPlayerName() == "" {
		gui.showNameDialog()
	} else {
		gui.showMenuScreen()
	}

	return gui
}

func (g *GUI) Run() error {
	g.window.ShowAndRun()
	return nil
}

func (g *GUI) showNameDialog() {
	nameEntry := widget.NewEntry()
	nameEntry.SetPlaceHolder("Введите ваше имя")

	okButton := widget.NewButton("OK", func() {})
	okButton.Importance = widget.HighImportance

	contentWithButton := container.NewVBox(
		widget.NewLabel("Добро пожаловать в Snake Multiplayer!"),
		widget.NewLabel(""),
		widget.NewLabel("Пожалуйста, введите ваше имя:"),
		nameEntry,
		widget.NewLabel(""),
		okButton,
	)

	d := dialog.NewCustom("Введите имя игрока", "", contentWithButton, g.window)

	okButton.OnTapped = func() {
		name := nameEntry.Text
		if name == "" {
			name = "Player"
		}
		g.client.SetPlayerName(name)
		d.Hide()
		g.showMenuScreen()
	}

	nameEntry.OnSubmitted = func(text string) {
		if text == "" {
			text = "Player"
		}
		g.client.SetPlayerName(text)
		d.Hide()
		g.showMenuScreen()
	}

	d.Show()

	g.window.Canvas().Focus(nameEntry)
}

func (g *GUI) showMenuScreen() {
	g.currentScreen = "menu"
	g.gameRenderer = nil

	title := widget.NewLabel("SNAKE MULTIPLAYER")
	title.Alignment = fyne.TextAlignCenter
	title.TextStyle.Bold = true

	playerLabel := widget.NewLabel(fmt.Sprintf("Игрок: %s", g.client.GetPlayerName()))
	playerLabel.Alignment = fyne.TextAlignCenter

	gamesLabel := widget.NewLabel("Доступные игры:")
	gamesLabel.TextStyle.Bold = true

	gamesList := widget.NewList(
		func() int {
			return len(g.client.GetAvailableGames())
		},
		func() fyne.CanvasObject {
			return widget.NewLabel("Game")
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			games := g.client.GetAvailableGames()
			i := 0
			for _, gameInfo := range games {
				if i == id {
					label := obj.(*widget.Label)
					label.SetText(fmt.Sprintf(" %s (%d игроков, %dx%d)",
						gameInfo.GameName, len(gameInfo.Players),
						gameInfo.Config.Width, gameInfo.Config.Height))
					break
				}
				i++
			}
		},
	)

	gamesList.OnSelected = func(id widget.ListItemID) {
		games := g.client.GetAvailableGames()
		i := 0
		for _, gameInfo := range games {
			if i == id {
				g.showJoinDialog(gameInfo)
				break
			}
			i++
		}
	}

	newGameBtn := widget.NewButton("Создать новую игру", func() {
		g.showCreateGameScreen()
	})
	newGameBtn.Importance = widget.HighImportance

	refreshBtn := widget.NewButton("Обновить список", func() {
		g.showMenuScreen()
	})

	quitBtn := widget.NewButton("Выход", func() {
		g.app.Quit()
	})

	content := container.NewVBox(
		layout.NewSpacer(),
		title,
		playerLabel,
		layout.NewSpacer(),
		gamesLabel,
		gamesList,
		layout.NewSpacer(),
		newGameBtn,
		refreshBtn,
		quitBtn,
		layout.NewSpacer(),
	)

	g.window.SetContent(container.NewPadded(content))
}

func (g *GUI) showCreateGameScreen() {
	g.currentScreen = "create"

	title := widget.NewLabel("Создать новую игру")
	title.Alignment = fyne.TextAlignCenter
	title.TextStyle.Bold = true

	nameEntry := widget.NewEntry()
	nameEntry.SetPlaceHolder("Моя игра")

	widthEntry := widget.NewEntry()
	widthEntry.SetPlaceHolder("40")
	widthEntry.SetText("40")

	heightEntry := widget.NewEntry()
	heightEntry.SetPlaceHolder("30")
	heightEntry.SetText("30")

	foodEntry := widget.NewEntry()
	foodEntry.SetPlaceHolder("1")
	foodEntry.SetText("1")

	delayEntry := widget.NewEntry()
	delayEntry.SetPlaceHolder("300")
	delayEntry.SetText("300")

	form := container.NewVBox(
		widget.NewLabel("Название игры:"),
		nameEntry,
		widget.NewLabel("Ширина поля (10-100):"),
		widthEntry,
		widget.NewLabel("Высота поля (10-100):"),
		heightEntry,
		widget.NewLabel("Еда (0-100):"),
		foodEntry,
		widget.NewLabel("Задержка (мс, 100-3000):"),
		delayEntry,
	)

	createBtn := widget.NewButton("Создать игру", func() {
		width := parseInt32(widthEntry.Text, -1)
		height := parseInt32(heightEntry.Text, -1)
		food := parseInt32(foodEntry.Text, -1)
		delay := parseInt32(delayEntry.Text, -1)

		var validationErrors []string

		if width < 10 || width > 100 {
			validationErrors = append(validationErrors, fmt.Sprintf("Ширина поля должна быть от 10 до 100 (введено: %s)", widthEntry.Text))
		}
		if height < 10 || height > 100 {
			validationErrors = append(validationErrors, fmt.Sprintf("Высота поля должна быть от 10 до 100 (введено: %s)", heightEntry.Text))
		}
		if food < 0 || food > 100 {
			validationErrors = append(validationErrors, fmt.Sprintf("Количество еды должно быть от 0 до 100 (введено: %s)", foodEntry.Text))
		}
		if delay < 100 || delay > 3000 {
			validationErrors = append(validationErrors, fmt.Sprintf("Задержка должна быть от 100 до 3000 мс (введено: %s)", delayEntry.Text))
		}

		if len(validationErrors) > 0 {
			errMsg := "Некорректные параметры игры:\n\n"
			for _, e := range validationErrors {
				errMsg += "• " + e + "\n"
			}
			dialog.ShowError(fmt.Errorf(errMsg), g.window)
			return
		}

		config := &protocol.GameConfig{
			Width:        width,
			Height:       height,
			FoodStatic:   food,
			StateDelayMs: delay,
		}

		gameName := nameEntry.Text
		if gameName == "" {
			gameName = "Моя игра"
		}

		err := g.client.StartNewGame(config, gameName)
		if err != nil {
			dialog.ShowError(err, g.window)
			return
		}

		g.startGameLoop()
		g.showGameScreen()
	})
	createBtn.Importance = widget.HighImportance

	cancelBtn := widget.NewButton("Отмена", func() {
		g.showMenuScreen()
	})

	content := container.NewVBox(
		layout.NewSpacer(),
		title,
		form,
		container.NewGridWithColumns(2, createBtn, cancelBtn),
		layout.NewSpacer(),
	)

	g.window.SetContent(container.NewPadded(content))
}

func (g *GUI) showJoinDialog(gameInfo *game.GameInfo) {
	viewerCheck := widget.NewCheck("Присоединиться как наблюдатель", func(bool) {})

	content := container.NewVBox(
		widget.NewLabel(fmt.Sprintf("Присоединиться к игре '%s'?", gameInfo.GameName)),
		widget.NewLabel(fmt.Sprintf("Игроков: %d", len(gameInfo.Players))),
		widget.NewLabel(fmt.Sprintf("Размер поля: %dx%d", gameInfo.Config.Width, gameInfo.Config.Height)),
		viewerCheck,
	)

	dialog.ShowCustomConfirm("Присоединиться к игре", "Присоединиться", "Отмена", content, func(join bool) {
		if join {
			g.joinErrorShown = false

			g.client.SetMasterAddress(gameInfo.MasterIP, gameInfo.MasterPort)
			g.client.SetGameConfig(&gameInfo.Config, gameInfo.GameName)
			g.network.SendJoin(gameInfo.GameName, viewerCheck.Checked)

			go func() {
				for i := 0; i < 30; i++ {
					time.Sleep(100 * time.Millisecond)

					if g.joinErrorShown {
						return
					}

					if g.client.GetPlayerID() != 0 {
						fyne.Do(func() {
							g.showGameScreen()
						})
						return
					}
				}
				fyne.Do(func() {
					if !g.joinErrorShown {
						dialog.ShowError(fmt.Errorf("Не удалось присоединиться к игре (сервер не отвечает)"), g.window)
					}
				})
			}()
		}
	}, g.window)
}

func (g *GUI) showGameScreen() {
	g.currentScreen = "game"
	g.deathDialogShown = false

	state := g.client.GetState()
	if state == nil {
		return
	}

	g.gameRenderer = NewGameRenderer(g.client, 20)
	gameView := container.NewScroll(g.gameRenderer)

	g.roleLabel = widget.NewLabel(fmt.Sprintf("Ваша роль: %s", roleToString(g.client.GetRole())))
	g.roleLabel.TextStyle.Bold = true

	g.playersList = widget.NewList(
		func() int {
			if state := g.client.GetState(); state != nil {
				count := 0
				for _, p := range state.Players {
					if p.Role != protocol.VIEWER {
						count++
					}
				}
				return count
			}
			return 0
		},
		func() fyne.CanvasObject {
			return widget.NewLabel("")
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if state := g.client.GetState(); state != nil {
				activeIdx := 0
				for _, player := range state.Players {
					if player.Role == protocol.VIEWER {
						continue
					}
					if activeIdx == id {
						label := obj.(*widget.Label)
						roleStr := roleToString(player.Role)
						label.SetText(fmt.Sprintf("%s (%s): %d ", player.Name, roleStr, player.Score))
						return
					}
					activeIdx++
				}
			}
		},
	)

	instructions := widget.NewLabel(" Управление: ↑↓←→ или WASD | ESC - стать наблюдателем | Q - выход")
	instructions.Alignment = fyne.TextAlignCenter

	leaveBtn := widget.NewButton("Покинуть игру", func() {
		g.leaveGame()
	})
	leaveBtn.Importance = widget.DangerImportance

	playersTitle := widget.NewLabel("Игроки:")
	playersTitle.TextStyle.Bold = true

	leftPanel := container.NewBorder(
		container.NewVBox(playersTitle, g.roleLabel),
		leaveBtn,
		nil, nil,
		g.playersList,
	)

	rightPanel := container.NewBorder(
		nil,
		instructions,
		nil, nil,
		gameView,
	)

	split := container.NewHSplit(leftPanel, rightPanel)
	split.SetOffset(0.25)

	g.window.SetContent(split)

}

func (g *GUI) startGameLoop() {
	if !g.client.IsMaster() {
		return
	}

	go func() {
		engine := g.client.GetEngine()
		if engine == nil {
			return
		}

		config := engine.GetConfig()
		delay := config.StateDelayMs
		if delay < 100 {
			delay = 300
		}

		ticker := time.NewTicker(time.Duration(delay) * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if !g.client.IsMaster() || engine == nil {
					return
				}

				gameActive := engine.Update()
				g.client.SetGameActive(gameActive)

				deadPlayers := engine.GetDeadPlayers()
				deadPlayersSet := make(map[int32]bool)
				for _, pid := range deadPlayers {
					deadPlayersSet[pid] = true
				}

				masterDied := deadPlayersSet[g.client.GetPlayerID()]

				var newMasterID int32 = 0

				if masterDied {
					g.client.UpdateState(engine.GetState())
					g.network.BroadcastState()

					deputyIP, _ := g.client.GetDeputyAddress()
					deputyID := g.client.GetDeputyID()
					oldDeputyAlive := deputyIP != "" && deputyID != 0 && !deadPlayersSet[deputyID]

					if oldDeputyAlive {
						newMasterID = deputyID
					} else {
						state := engine.GetState()
						for _, p := range state.Players {
							if p.Role == protocol.NORMAL && !deadPlayersSet[p.ID] {
								ip, port := g.network.GetPlayerAddress(p.ID)
								if ip != "" {
									g.client.PromoteNormalToDeputy(p.ID)
									g.network.SetDeputyInfo(p.ID, ip, port)
									newMasterID = p.ID
									break
								}
							}
						}
					}

					if newMasterID != 0 {
						g.network.SendMasterRoleWithState(newMasterID)
						time.Sleep(20 * time.Millisecond)
					}
				}

				for _, playerID := range deadPlayers {
					if playerID != g.client.GetPlayerID() && playerID != newMasterID {
						g.network.SendRoleChange(playerID, protocol.MASTER, protocol.VIEWER)
					}
				}

				if masterDied {
					if newMasterID != 0 {
						g.client.SetRole(protocol.VIEWER)

						state := engine.GetState()
						for i := range state.Players {
							if state.Players[i].ID == g.client.GetPlayerID() {
								state.Players[i].Role = protocol.VIEWER
								break
							}
						}

						fyne.Do(func() {
							g.deathDialogShown = true
							dialog.ShowInformation("Вы погибли",
								"Ваша змейка погибла! Вы теперь наблюдатель.", g.window)
						})
					} else {
						g.network.BroadcastGameOver()
						g.client.SetGameActive(false)
						fyne.Do(func() {
							dialog.ShowInformation("Игра завершена",
								"Вы погибли и игра завершена.", g.window)
							g.leaveGame()
						})
					}
					return
				}

				g.client.UpdateState(engine.GetState())
				g.network.BroadcastState()

				if !gameActive {
					g.network.BroadcastGameOver()
					fyne.Do(func() {
						dialog.ShowInformation("Игра завершена",
							"Все игроки покинули игру. Игра завершена.", g.window)
						g.leaveGame()
					})
					return
				}
			}
		}
	}()
}

func (g *GUI) startGameLoopWithEngine(engine *game.Engine) {
	if engine == nil {
		return
	}

	go func() {
		config := engine.GetConfig()
		delay := config.StateDelayMs
		if delay < 100 {
			delay = 300
		}

		ticker := time.NewTicker(time.Duration(delay) * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if !g.client.IsMaster() || g.client.GetEngine() == nil {
					return
				}

				gameActive := engine.Update()
				g.client.SetGameActive(gameActive)

				deadPlayers := engine.GetDeadPlayers()
				deadPlayersSet := make(map[int32]bool)
				for _, pid := range deadPlayers {
					deadPlayersSet[pid] = true
				}

				masterDied := deadPlayersSet[g.client.GetPlayerID()]

				var newMasterID int32 = 0

				if masterDied {
					g.client.UpdateState(engine.GetState())
					g.network.BroadcastState()

					deputyIP, _ := g.client.GetDeputyAddress()
					deputyID := g.client.GetDeputyID()
					oldDeputyAlive := deputyIP != "" && deputyID != 0 && !deadPlayersSet[deputyID]

					if oldDeputyAlive {
						newMasterID = deputyID
					} else {
						state := engine.GetState()
						for _, p := range state.Players {
							if p.Role == protocol.NORMAL && !deadPlayersSet[p.ID] {
								ip, port := g.network.GetPlayerAddress(p.ID)
								if ip != "" {
									g.client.PromoteNormalToDeputy(p.ID)
									g.network.SetDeputyInfo(p.ID, ip, port)
									newMasterID = p.ID
									break
								}
							}
						}
					}

					if newMasterID != 0 {
						g.network.SendMasterRoleWithState(newMasterID)
						time.Sleep(20 * time.Millisecond)
					}
				}

				for _, playerID := range deadPlayers {
					if playerID != g.client.GetPlayerID() && playerID != newMasterID {
						g.network.SendRoleChange(playerID, protocol.MASTER, protocol.VIEWER)
					}
				}

				if masterDied {
					if newMasterID != 0 {
						g.client.SetRole(protocol.VIEWER)

						state := engine.GetState()
						for i := range state.Players {
							if state.Players[i].ID == g.client.GetPlayerID() {
								state.Players[i].Role = protocol.VIEWER
								break
							}
						}

						fyne.Do(func() {
							g.deathDialogShown = true
							dialog.ShowInformation("Вы погибли",
								"Ваша змейка погибла! Вы теперь наблюдатель.", g.window)
						})
					} else {
						// Некому передать роль - завершаем игру
						g.network.BroadcastGameOver()
						g.client.SetGameActive(false)
						fyne.Do(func() {
							dialog.ShowInformation("Игра завершена",
								"Вы погибли и игра завершена.", g.window)
							g.leaveGame()
						})
					}
					return
				}

				g.client.UpdateState(engine.GetState())
				g.network.BroadcastState()

				if !gameActive {
					g.network.BroadcastGameOver()
					fyne.Do(func() {
						dialog.ShowInformation("Игра завершена",
							"Все игроки покинули игру. Игра завершена.", g.window)
						g.leaveGame()
					})
					return
				}
			}
		}
	}()
}

func (g *GUI) leaveGame() {
	if g.client.IsMaster() {
		engine := g.client.GetEngine()
		if engine != nil {
			state := engine.GetState()
			activeCount := 0
			for _, player := range state.Players {
				if player.ID != g.client.GetPlayerID() && player.Role != protocol.VIEWER {
					activeCount++
				}
			}

			if activeCount == 0 {
				g.network.BroadcastGameOver()
			} else {
				deputyIP, _ := g.client.GetDeputyAddress()
				if deputyIP != "" {
					g.network.TransferMasterRole()
				}
			}
		}
	}

	g.gameRenderer = nil
	g.playersList = nil
	g.roleLabel = nil
	g.deathDialogShown = false
	g.client.LeaveGame()
	g.showMenuScreen()
}

func (g *GUI) becomeViewer() {
	if g.client.GetRole() == protocol.VIEWER {
		return
	}

	if g.client.IsMaster() {
		engine := g.client.GetEngine()
		if engine != nil {
			state := engine.GetState()
			activeCount := 0
			for _, player := range state.Players {
				if player.ID != g.client.GetPlayerID() && player.Role != protocol.VIEWER {
					activeCount++
				}
			}

			if activeCount == 0 {
				g.network.BroadcastGameOver()
				g.client.SetGameActive(false)
				dialog.ShowInformation("Игра завершена",
					"Вы вышли из игры и она завершена.", g.window)
				g.leaveGame()
				return
			}

			deputyIP, _ := g.client.GetDeputyAddress()
			if deputyIP != "" {
				g.network.TransferMasterRole()
			}

			engine.MakePlayerZombie(g.client.GetPlayerID())
		}
	}

	g.network.RequestViewerMode()

	g.client.SetRole(protocol.VIEWER)

	if g.roleLabel != nil {
		g.roleLabel.SetText(fmt.Sprintf("Ваша роль: %s", roleToString(protocol.VIEWER)))
	}
}

func parseInt32(s string, defaultVal int32) int32 {
	var val int32
	_, err := fmt.Sscanf(s, "%d", &val)
	if err != nil || val == 0 {
		return defaultVal
	}
	return val
}

func roleToString(role protocol.NodeRole) string {
	switch role {
	case protocol.MASTER:
		return "MASTER"
	case protocol.DEPUTY:
		return "DEPUTY"
	case protocol.NORMAL:
		return "NORMAL"
	case protocol.VIEWER:
		return "VIEWER"
	default:
		return "❓"
	}
}
