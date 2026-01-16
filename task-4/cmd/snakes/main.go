package main

import (
	"flag"
	"fmt"
	"os"

	"snakes/pkg/game"
	"snakes/pkg/network"
	"snakes/pkg/ui"
)

func main() {
	playerName := flag.String("name", "", "Player name (optional, will be asked in GUI)")
	flag.Parse()

	name := *playerName

	client := game.NewClient(name)

	netManager := network.NewManager(client)
	if err := netManager.Start(); err != nil {
		fmt.Printf("Failed to start network: %v\n", err)
		os.Exit(1)
	}
	defer netManager.Stop()

	gameUI := ui.NewGUI(client, netManager)
	if err := gameUI.Run(); err != nil {
		fmt.Printf("UI error: %v\n", err)
		os.Exit(1)
	}
}
