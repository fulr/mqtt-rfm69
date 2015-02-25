package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"

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
	Type   int16  // sensor type (2, 3, 4, 5)
	Uptime uint32 // uptime in ms
}

type payload6 struct {
	Temperature float32 // Temp
	Humidity    float32 // Humidity
	VBat        float32 // V Battery
}

var f = func(client *MQTT.MqttClient, msg MQTT.Message) {
	fmt.Printf("TOPIC: %s\n", msg.Topic())
	fmt.Printf("MSG: %s\n", msg.Payload())
}

func actorHandler(txChan chan rfm69.Data) func(client *MQTT.MqttClient, msg MQTT.Message) {
	return func(client *MQTT.MqttClient, msg MQTT.Message) {
		command := string(msg.Payload())
		log.Println(msg.Topic(), command)

		on := byte(0)
		if command == "ON" {
			on = 1
		}

		parts := strings.Split(msg.Topic(), "/")

		node, err := strconv.Atoi(parts[2])
		if err != nil {
			log.Println(err)
			return
		}
		pin, err := strconv.Atoi(parts[3])
		if err != nil {
			log.Println(err)
			return
		}

		txChan <- rfm69.Data{
			ToAddress:  byte(node),
			Data:       []byte{byte(pin), on},
			RequestAck: true,
		}
	}
}

func main() {
	log.Print("Start")

	opts := MQTT.NewClientOptions().AddBroker(mqttBroker).SetClientId(clientID)
	opts.SetDefaultPublishHandler(f)
	opts.SetCleanSession(true)

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

	rxChan, txChan, quit := rfm.Loop()

	sigint := make(chan os.Signal, 1)
	signal.Notify(sigint, os.Interrupt, os.Kill)

	topicFilter, err := MQTT.NewTopicFilter("/actor/#", 0)
	if err != nil {
		log.Fatal(err)
	}

	receipt, err := c.StartSubscription(actorHandler(txChan), topicFilter)

	if err != nil {
		log.Fatal(err)
	}

	<-receipt

	for {
		select {
		case data := <-rxChan:
			var p payload
			buf := bytes.NewReader(data.Data)
			binary.Read(buf, binary.LittleEndian, &p)
			log.Println("payload", p)
			topic := fmt.Sprintf("/sensor/%d/", data.FromAddress)
			receipt := c.Publish(MQTT.QOS_ZERO, topic+"rssi", fmt.Sprintf("%d", data.Rssi))
			<-receipt
			switch p.Type {
			case 6:
				var p6 payload6
				binary.Read(buf, binary.LittleEndian, &p6)
				log.Println("payload6", p6)
				receipt = c.Publish(MQTT.QOS_ZERO, topic+"temp", fmt.Sprintf("%f", p6.Temperature))
				<-receipt
				receipt = c.Publish(MQTT.QOS_ZERO, topic+"hum", fmt.Sprintf("%f", p6.Humidity))
				<-receipt
				receipt = c.Publish(MQTT.QOS_ZERO, topic+"bat", fmt.Sprintf("%f", p6.VBat))
				<-receipt
			}

		case <-sigint:
			quit <- 1
			<-quit
			c.Disconnect(250)
			return
		}
	}
}
