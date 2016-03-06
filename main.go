package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	MQTT "github.com/eclipse/paho.mqtt.golang"
	"github.com/fulr/mqtt-rfm69/payload"
	"github.com/fulr/rfm69"
)

// Configuration defines the config options and file structure

var defautlPubHandler = func(client *MQTT.Client, msg MQTT.Message) {
	fmt.Printf("TOPIC: %s\n", msg.Topic())
	fmt.Printf("MSG: %s\n", msg.Payload())
}

func actorHandler(r *rfm69.Device) func(client *MQTT.Client, msg MQTT.Message) {
	return func(client *MQTT.Client, msg MQTT.Message) {
		command := string(msg.Payload())
		fmt.Println(msg.Topic(), command)
		on := byte(0)
		if command == "ON" {
			on = 1
		}
		parts := strings.Split(msg.Topic(), "/")
		node, err := strconv.Atoi(parts[2])
		if err != nil {
			fmt.Println(err)
			return
		}
		pin, err := strconv.Atoi(parts[3])
		if err != nil {
			fmt.Println(err)
			return
		}
		buf := bytes.Buffer{}
		binary.Write(&buf, binary.LittleEndian, payload.Payload{Type: 2, Uptime: 1})
		binary.Write(&buf, binary.LittleEndian, payload.Payload2{Pin: byte(pin), State: on})
		r.Send(&rfm69.Data{
			ToAddress:  byte(node),
			Data:       buf.Bytes(),
			RequestAck: true,
		})
	}
}

func pubValue(c *MQTT.Client, topic string, suffix string, value float32) {
	token := c.Publish(topic+suffix, 0, false, fmt.Sprintf("%f", value))
	if token.WaitTimeout(5*time.Second) && token.Error() != nil {
		fmt.Println("publish error:", token.Error())
	}
}

func main() {
	encryptionKey := flag.String("key", "0123456789012345", "Encryption key for the RFM69 module")
	nodeID := flag.Int("nodeid", 1, "NodeID in the RFM69 network")
	networkID := flag.Int("networkid", 73, "RFM69 network ID")
	isRfm69Hw := flag.Bool("ishw", true, "Enable RFM69HW high power mode")
	mqttBroker := flag.String("broker", "tcp://localhost:1883", "MQTT broker URL")
	mqttClientID := flag.String("clientid", "rfmGate", "MQTT client ID")
	topicPrefix := flag.String("prefix", "home", "MQTT topic prefix")
	flag.Parse()

	rfm, err := rfm69.NewDevice(byte(*nodeID), byte(*networkID), *isRfm69Hw)
	if err != nil {
		panic(err)
	}
	defer rfm.Close()

	rx := make(chan *rfm69.Data, 5)
	rfm.OnReceive = func(d *rfm69.Data) {
		rx <- d
	}

	err = rfm.Encrypt([]byte(*encryptionKey))
	if err != nil {
		panic(err)
	}

	opts := MQTT.NewClientOptions()
	opts.AddBroker(*mqttBroker)
	opts.SetClientID(*mqttClientID)
	opts.SetDefaultPublishHandler(defautlPubHandler)
	opts.SetCleanSession(true)
	opts.OnConnect = func(c *MQTT.Client) {
		fmt.Println("MQTT connect")
		c.Subscribe(strings.Join([]string{*topicPrefix, "actor", "#"}, "/"), 0, actorHandler(rfm))
	}

	c := MQTT.NewClient(opts)
	token := c.Connect()
	if token.WaitTimeout(5*time.Second) && token.Error() != nil {
		panic(token.Error())
	}

	sigint := make(chan os.Signal, 1)
	signal.Notify(sigint, os.Interrupt, os.Kill)

	running := true

	for running {
		select {
		case data := <-rx:
			if data.ToAddress != byte(*nodeID) {
				break
			}
			fmt.Println("got data from", data.FromAddress, ", RSSI", data.Rssi)
			if data.ToAddress != 255 && data.RequestAck {
				rfm.Send(data.ToAck())
			}
			topic := fmt.Sprintf("%s/sensor/%d/", *topicPrefix, data.FromAddress)
			pubValue(c, topic, "rssi", float32(data.Rssi))
			if len(data.Data) > 5 {
				var p payload.Payload
				buf := bytes.NewReader(data.Data)
				binary.Read(buf, binary.LittleEndian, &p)
				fmt.Println("payload", p)
				switch p.Type {
				case 1:
					var p1 payload.Payload1
					binary.Read(buf, binary.LittleEndian, &p1)
					fmt.Println("payload1", p1)
					pubValue(c, topic, "temp", p1.Temperature)
					pubValue(c, topic, "hum", p1.Humidity)
					pubValue(c, topic, "bat", p1.VBat)
				default:
					fmt.Println("unknown payload")
				}
			}
		case <-sigint:
			running = false
		}
	}
	c.Disconnect(250)
}
