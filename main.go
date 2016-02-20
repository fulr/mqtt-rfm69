package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
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
type Configuration struct {
	EncryptionKey string
	NodeID        byte
	NetworkID     byte
	IsRfm69Hw     bool
	MqttBroker    string
	MqttClientID  string
	TopicPrefix   string
}

var defautlPubHandler = func(client *MQTT.Client, msg MQTT.Message) {
	fmt.Printf("TOPIC: %s\n", msg.Topic())
	fmt.Printf("MSG: %s\n", msg.Payload())
}

func actorHandler(tx chan *rfm69.Data) func(client *MQTT.Client, msg MQTT.Message) {
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
		tx <- &rfm69.Data{
			ToAddress:  byte(node),
			Data:       buf.Bytes(),
			RequestAck: true,
		}
	}
}

func readConfig() (*Configuration, error) {
	file, err := os.Open("/etc/rfm69/conf.json")
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(file)
	config := &Configuration{}
	err = decoder.Decode(config)
	file.Close()
	return config, err
}

func pubValue(c *MQTT.Client, topic string, suffix string, value float32) {
	token := c.Publish(topic+suffix, 0, false, fmt.Sprintf("%f", value))
	if token.Wait() && token.Error() != nil {
		fmt.Println("publish error:", token.Error())
	}
}

func main() {
	fmt.Println("Reading config")
	config, err := readConfig()
	if err != nil {
		panic(err)
	}
	fmt.Println(config)
	opts := MQTT.NewClientOptions().AddBroker(config.MqttBroker).SetClientID(config.MqttClientID)
	opts.SetDefaultPublishHandler(defautlPubHandler)
	opts.SetCleanSession(true)
	c := MQTT.NewClient(opts)
	token := c.Connect()
	if token.Wait() && token.Error() != nil {
		panic(token.Error())
	}
	rfm, err := rfm69.NewDevice(config.NodeID, config.NetworkID, config.IsRfm69Hw)
	if err != nil {
		panic(err)
	}
	defer rfm.Close()
	err = rfm.Encrypt([]byte(config.EncryptionKey))
	if err != nil {
		panic(err)
	}
	rx, tx, quit := rfm.Loop()

	sigint := make(chan os.Signal, 1)
	signal.Notify(sigint, os.Interrupt, os.Kill)

	actorTopic := fmt.Sprintf("%s/actor/#", config.TopicPrefix)
	token = c.Subscribe(actorTopic, 0, actorHandler(tx))
	if token.Wait() && token.Error() != nil {
		panic(token.Error())
	}
	defer c.Unsubscribe(actorTopic)

	timeout := time.After(time.Hour)

	for {
		select {
		case data := <-rx:
			if data.ToAddress != config.NodeID {
				break
			}
			fmt.Println("got data from", data.FromAddress, ", RSSI", data.Rssi)
			if data.ToAddress != 255 && data.RequestAck {
				tx <- data.ToAck()
			}
			topic := fmt.Sprintf("%s/sensor/%d/", config.TopicPrefix, data.FromAddress)
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
			quit <- true
			<-quit
			c.Disconnect(250)
			return
		case <-timeout:
			quit <- true
			<-quit
			c.Disconnect(250)
			return
		}
	}
}
