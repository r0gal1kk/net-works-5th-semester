package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"time"
)

const maxNameBytes = 4096
const maxFileSize = uint64(1 << 40)

func readHandler(r io.Reader) (name string, size int64, err error) {
	// Приветственное сообщение
	helloMessage := make([]byte, 4)
	_, err = io.ReadFull(r, helloMessage)
	if err != nil {
		return "", 0, err
	}
	if string(helloMessage) != "OHIO" {
		return "", 0, errors.New("expected OHIO")
	}
	// Размер имени
	buffNameSize := make([]byte, 2)
	_, err = io.ReadFull(r, buffNameSize)
	if err != nil {
		return "", 0, err
	}
	nameSize := int(binary.BigEndian.Uint16(buffNameSize))
	if nameSize == 0 || nameSize > maxNameBytes {
		return "", 0, errors.New("invalid size of name")
	}
	// Имя
	buffName := make([]byte, nameSize)
	_, err = io.ReadFull(r, buffName)
	if err != nil {
		return "", 0, err
	}

	name = string(buffName)
	// Размер файла
	buffSize := make([]byte, 8)
	_, err = io.ReadFull(r, buffSize)
	if err != nil {
		return "", 0, err
	}
	size = int64(binary.BigEndian.Uint64(buffSize))
	return name, size, nil
}

func clientHandler(conn net.Conn) {
	defer func() {
		err := conn.Close()
		if err != nil {
			fmt.Println("Error closing connection:", err)
		}
	}()
	name, size, err := readHandler(conn)
	if err != nil {
		fmt.Println(err)
		return
	}
	err = os.MkdirAll("uploads", 0755)
	if err != nil {
		fmt.Println(err)
		return
	}
	filePath := filepath.Join("uploads", filepath.Base(name))
	file, err := os.Create(filePath)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() {
		err := file.Close()
		if err != nil {
			fmt.Println(err)
			err := os.Remove(filePath)
			if err != nil {
				fmt.Println(err)
			}
		}
	}()

	start := time.Now()
	last := start
	remaining := size
	total := 0
	period := 0

	buffer := make([]byte, 1024*32)
	for remaining > 0 {
		toRead := int64(len(buffer))
		if remaining < toRead {
			toRead = remaining
		}
		nRead, err := io.ReadFull(conn, buffer[:toRead])
		if err != nil {
			fmt.Println(err)
			return
		}
		nWritten, err := file.Write(buffer[:nRead])
		if err != nil {
			fmt.Println(err)
			return
		}

		total += nWritten
		period += nWritten
		remaining -= int64(nWritten)

		now := time.Now()
		if now.Sub(last) >= 3*time.Second {
			instantSpeed := float64(period) / (now.Sub(last).Seconds() * 1024 * 1024)
			avgSpeed := float64(total) / (now.Sub(start).Seconds() * 1024 * 1024)
			fmt.Printf("[%s] time  %.2f  instant: %.2f MB/s, avg: %.2f MB/s\n",
				conn.RemoteAddr(), now.Sub(start).Seconds(), instantSpeed, avgSpeed)
			last = now
			period = 0
		}
	}
	elapsed := time.Since(start).Seconds()
	avgSpeed := float64(total) / (math.Max(elapsed, 1e-6) * 1024 * 1024)
	instantSpeed := float64(period) / (math.Max(elapsed, 1e-6) * 1024 * 1024)
	fmt.Printf("[%s] instant: %.2f MB/s, avg: %.2f MB/s (final)\n", conn.RemoteAddr(), instantSpeed, avgSpeed)
	var status byte = 0
	if int64(total) == size {
		status = 1
	}
	_, err = conn.Write([]byte{status})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("File saved to:", filePath)
}

func main() {
	if len(os.Args) != 2 {
		fmt.Println("Usage: server <port>")
		os.Exit(1)
	}
	port := os.Args[1]
	listener, err := net.Listen("tcp", ":"+port)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	defer func() {
		err := listener.Close()
		if err != nil {
			fmt.Println("Error closing connection:", err)
			os.Exit(1)
		}
	}()
	for {
		clientConn, err := listener.Accept()
		if err != nil {
			fmt.Println(err)
			continue
		}
		go clientHandler(clientConn)
	}

}
