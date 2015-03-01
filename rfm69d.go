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
	"github.com/fulr/rfm69"
)

const (
	encryptionKey = "0123456789012345"
	nodeID        = 1
	networkID     = 73
	isRfm69Hw     = true
	mqttBroker    = "tcp://localhost:1883"
	clientID      = "rfmGate"
)

type payload struct {
	Type   int16  // sensor type (2, 3, 4, 5)
	Uptime uint32 // uptime in ms
}

type payload1 struct {
	Temperature float32 // Temp
	Humidity    float32 // Humidity
	VBat        float32 // V Battery
}

var f = func(client *MQTT.MqttClient, msg MQTT.Message) {
	fmt.Printf("TOPIC: %s\n", msg.Topic())
	fmt.Printf("MSG: %s\n", msg.Payload())
}

func actorHandler(tx chan *rfm69.Data) func(client *MQTT.MqttClient, msg MQTT.Message) {
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
		tx <- &rfm69.Data{
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
	rfm, err := rfm69.NewDevice(nodeID, networkID, isRfm69Hw)
	if err != nil {
		log.Fatal(err)
	}
	defer rfm.Close()
	err = rfm.Encrypt([]byte(encryptionKey))
	if err != nil {
		log.Fatal(err)
	}
	rx, tx, quit := rfm.Loop()

	sigint := make(chan os.Signal, 1)
	signal.Notify(sigint, os.Interrupt, os.Kill)

	topicFilter, err := MQTT.NewTopicFilter("/actor/#", 0)
	if err != nil {
		log.Fatal(err)
	}
	receipt, err := c.StartSubscription(actorHandler(tx), topicFilter)
	if err != nil {
		log.Fatal(err)
	}
	<-receipt
	defer c.EndSubscription("/actor/#")

	for {
		select {
		case data := <-rx:
			if data.ToAddress != nodeID {
				break
			}
			log.Println("got data from", data.FromAddress, ", RSSI", data.Rssi)
			if data.ToAddress != 255 && data.RequestAck {
				tx <- data.ToAck()
			}
			topic := fmt.Sprintf("/sensor/%d/", data.FromAddress)
			receipt := c.Publish(MQTT.QOS_ZERO, topic+"rssi", fmt.Sprintf("%d", data.Rssi))
			<-receipt
			if len(data.Data) > 5 {
				var p payload
				buf := bytes.NewReader(data.Data)
				binary.Read(buf, binary.LittleEndian, &p)
				log.Println("payload", p)
				switch p.Type {
				case 1:
					var p1 payload1
					binary.Read(buf, binary.LittleEndian, &p1)
					log.Println("payload1", p1)
					receipt = c.Publish(MQTT.QOS_ZERO, topic+"temp", fmt.Sprintf("%f", p1.Temperature))
					<-receipt
					receipt = c.Publish(MQTT.QOS_ZERO, topic+"hum", fmt.Sprintf("%f", p1.Humidity))
					<-receipt
					receipt = c.Publish(MQTT.QOS_ZERO, topic+"bat", fmt.Sprintf("%f", p1.VBat))
					<-receipt
				default:
					log.Println("unknown payload")
				}
			}
		case <-sigint:
			quit <- true
			<-quit
			c.Disconnect(250)
			return
		}
	}
}
