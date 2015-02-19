package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"os/signal"

	MQTT "git.eclipse.org/gitroot/paho/org.eclipse.paho.mqtt.golang.git"
	"github.com/davecheney/gpio"
	"github.com/fulr/rfm69"
	"github.com/fulr/spidev"
)

const (
	irqPin        = gpio.GPIO25
	encryptionKey = "0123456789012345"
	nodeID        = 1
	networkID     = 73
	isRfm69Hw     = true
	spiPath       = "/dev/spidev0.0"
	mqttBroker    = "tcp://localhost:1883"
	clientID      = "rfmGate"
)

type payload struct {
	Type int16   //sensor ID (2, 3, 4, 5)
	Var1 uint32  //uptime in ms
	Var2 float32 //sensor data?
	Var3 float32 //battery condition?
}

var f = func(client *MQTT.MqttClient, msg MQTT.Message) {
	fmt.Printf("TOPIC: %s\n", msg.Topic())
	fmt.Printf("MSG: %s\n", msg.Payload())
}

func main() {
	log.Print("Start")

	opts := MQTT.NewClientOptions().AddBroker(mqttBroker).SetClientId(clientID)
	opts.SetDefaultPublishHandler(f)

	c := MQTT.NewClient(opts)
	_, err := c.Start()
	if err != nil {
		panic(err)
	}

	pin, err := gpio.OpenPin(irqPin, gpio.ModeInput)
	if err != nil {
		panic(err)
	}
	defer pin.Close()

	spiBus, err := spidev.NewSPIDevice(spiPath)
	if err != nil {
		panic(err)
	}
	defer spiBus.Close()

	rfm, err := rfm69.NewDevice(spiBus, pin, nodeID, networkID, isRfm69Hw)
	if err != nil {
		log.Fatal(err)
	}
	log.Print(rfm)

	err = rfm.Encrypt([]byte(encryptionKey))
	if err != nil {
		log.Fatal(err)
	}

	rxChan, _, quit := rfm.Loop()

	sigint := make(chan os.Signal, 1)
	signal.Notify(sigint, os.Interrupt, os.Kill)

	var p payload

	for {
		select {
		case data := <-rxChan:
			log.Print("main got data")
			log.Print(data)
			buf := bytes.NewReader(data.Data)
			binary.Read(buf, binary.LittleEndian, &p)
			log.Println(p)
			topic := fmt.Sprintf("/sensor/%d/%d", data.FromAddress, 1)
			receipt := c.Publish(MQTT.QOS_ZERO, topic, fmt.Sprintf("%f", p.Var2))
			<-receipt
		case <-sigint:
			quit <- 1
			<-quit
			return
		}
	}
}
