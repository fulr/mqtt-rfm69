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

	"github.com/fulr/mqtt-rfm69/payload"
	"github.com/fulr/rfm69"
	"github.com/yosssi/gmq/mqtt"
	"github.com/yosssi/gmq/mqtt/client"
)

// Configuration defines the config options and file structure

func actorHandler(r *rfm69.Device) func(topic, message []byte) {
	return func(topic, message []byte) {
		command := string(message)
		topicstr := string(topic)
		fmt.Println(topicstr, command)
		on := byte(0)
		if command == "ON" {
			on = 1
		}
		parts := strings.Split(topicstr, "/")
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

func pubValue(c *client.Client, topic string, suffix string, value float32) error {
	return c.Publish(&client.PublishOptions{
		QoS:       mqtt.QoS0,
		TopicName: []byte(topic + suffix),
		Message:   []byte(fmt.Sprintf("%f", value)),
	})
}

func main() {
	encryptionKey := flag.String("key", "0123456789012345", "Encryption key for the RFM69 module")
	nodeID := flag.Int("nodeid", 1, "NodeID in the RFM69 network")
	networkID := flag.Int("networkid", 73, "RFM69 network ID")
	isRfm69Hw := flag.Bool("ishw", true, "Enable RFM69HW high power mode")
	mqttBroker := flag.String("broker", "localhost:1883", "MQTT broker URL")
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

	cliOpts := client.Options{
		ErrorHandler: func(err error) {
			fmt.Println(err)
		},
	}
	connOpts := client.ConnectOptions{
		Network:  "tcp",
		Address:  *mqttBroker,
		ClientID: []byte(*mqttClientID),
	}
	cli := client.New(&cliOpts)
	defer cli.Terminate()

	err = cli.Connect(&connOpts)
	if err != nil {
		panic(err)
	}

	err = cli.Subscribe(&client.SubscribeOptions{
		SubReqs: []*client.SubReq{
			&client.SubReq{
				TopicFilter: []byte(strings.Join([]string{*topicPrefix, "actor", "#"}, "/")),
				QoS:         mqtt.QoS0,
				Handler:     actorHandler(rfm),
			},
		},
	})
	if err != nil {
		panic(err)
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
			pubValue(cli, topic, "rssi", float32(data.Rssi))
			if len(data.Data) > 5 {
				var p payload.Payload
				buf := bytes.NewReader(data.Data)
				err = binary.Read(buf, binary.LittleEndian, &p)
				if err != nil {
					fmt.Println("error reading paylod")
					break
				}
				fmt.Println("payload", p)
				switch p.Type {
				case 1:
					var p1 payload.Payload1
					err = binary.Read(buf, binary.LittleEndian, &p1)
					if err != nil {
						fmt.Println("error reading paylod")
						break
					}
					fmt.Println("payload1", p1)
					pubValue(cli, topic, "temp", p1.Temperature)
					pubValue(cli, topic, "hum", p1.Humidity)
					pubValue(cli, topic, "bat", p1.VBat)
				default:
					fmt.Println("unknown payload")
				}
			}
		case <-sigint:
			running = false
		}
	}
}
