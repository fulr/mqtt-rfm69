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
	Type int16   // sensor type (2, 3, 4, 5)
	Var1 uint32  // uptime in ms
	Var2 float32 // Temp
	Var3 float32 // Humidity
	VBat float32 // V Battery
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
			buf := bytes.NewReader(data.Data)
			binary.Read(buf, binary.LittleEndian, &p)
			log.Println("payload", p)
			topic := fmt.Sprintf("/sensor/node%d_", data.FromAddress)
			receipt := c.Publish(MQTT.QOS_ZERO, topic+"rssi", fmt.Sprintf("%d", data.Rssi))
			<-receipt
			switch p.Type {
			case 6:
				receipt := c.Publish(MQTT.QOS_ZERO, topic+"temp", fmt.Sprintf("%f", p.Var2))
				<-receipt
				receipt = c.Publish(MQTT.QOS_ZERO, topic+"hum", fmt.Sprintf("%f", p.Var3))
				<-receipt
				receipt = c.Publish(MQTT.QOS_ZERO, topic+"bat", fmt.Sprintf("%f", p.VBat))
				<-receipt
			}

		case <-sigint:
			quit <- 1
			<-quit
			return
		}
	}
}
