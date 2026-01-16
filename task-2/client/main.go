package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
)

const maxNameBytes = 4096
const maxFileSize = uint64(1 << 40)

func sendHandler(w io.Writer, name []byte, size uint64) error {
	//Приветственное сообщение
	helloMessage := [4]byte{'O', 'H', 'I', 'O'}
	nHello, err := w.Write(helloMessage[:])
	if err != nil {
		return err
	}
	if nHello != len(helloMessage) {
		return errors.New(fmt.Sprintf("expected in Hello to write %d bytes, but wrote %d", len(helloMessage), nHello))
	}
	//Размер имени
	bufferName := make([]byte, 2)
	binary.BigEndian.PutUint16(bufferName, uint16(len(name)))
	nNameSize, err := w.Write(bufferName)
	if err != nil {
		return err
	}
	if len(bufferName) != nNameSize {
		return errors.New(fmt.Sprintf("expected in NameSize to write %d bytes, but wrote %d", len(bufferName), nNameSize))
	}
	//Имя
	nName, err := w.Write(name)
	if err != nil {
		return err
	}
	if nName != len(name) {
		return errors.New(fmt.Sprintf("expected in Name to write %d bytes, but wrote %d", len(name), nName))
	}
	//Размер файла
	bufferSize := make([]byte, 8)
	binary.BigEndian.PutUint64(bufferSize, size)
	nSize, err := w.Write(bufferSize)
	if err != nil {
		return err
	}
	if nSize != len(bufferSize) {
		return errors.New(fmt.Sprintf("expected in NameSize to write %d bytes, but wrote %d", len(bufferName), nNameSize))
	}
	return nil
}

func main() {
	if len(os.Args) != 4 {
		fmt.Println("Usage: client <file-path> <server-host> <server-port>")
		os.Exit(1)
	}
	filePath := os.Args[1]
	serverHost := os.Args[2]
	serverPort := os.Args[3]

	fileInfo, err := os.Stat(filePath)
	if err != nil {
		fmt.Println("File does not exist")
		os.Exit(1)
	}
	if uint64(fileInfo.Size()) > maxFileSize {
		fmt.Println("File size too big")
		os.Exit(1)
	}
	if fileInfo.IsDir() {
		fmt.Println("File is a directory")
		os.Exit(1)
	}
	name := []byte(filepath.Base(filePath))
	if len(name) > maxNameBytes {
		fmt.Println("File name too long")
		os.Exit(1)
	}

	conn, err := net.Dial("tcp", net.JoinHostPort(serverHost, serverPort))
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer func() {
		err := conn.Close()
		if err != nil {
			fmt.Println("Error closing connection:", err)
		}
	}()

	err = sendHandler(conn, name, uint64(fileInfo.Size()))
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	file, err := os.Open(filePath)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer func() {
		err := file.Close()
		if err != nil {
			fmt.Println("Error closing file:", err)
		}
	}()
	_, err = io.Copy(conn, file)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	status := make([]byte, 1)
	if _, err := io.ReadFull(conn, status); err != nil {
		fmt.Println("Failed to read server response")
		return
	}

	if status[0] == 1 {
		fmt.Println("File sent successfully")
	} else {
		fmt.Println("File transfer failed")
	}
}
