package main

import (
	"encoding/xml"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/fsnotify/fsnotify"
	"github.com/joho/godotenv"
)

// describe mrefd.xml
type Reflector struct {
	Callsign string    `xml:"CALLSIGN,attr"`
	Version  string    `xml:"VERSION"`
	Peers    []Peer    `xml:"PEERS"`
	Nodes    []Node    `xml:"NODES"`
	Stations []Station `xml:"STATIONS>STATION"`
}

type Peer struct {
}

type Node struct {
	Callsign      string    `xml:"CALLSIGN"`
	IP            string    `xml:"IP"`
	LinkedModule  string    `xml:"LINKEDMODULE"`
	Protocol      string    `xml:"PROTOCOL"`
	ConnectTime   time.Time `xml:"CONNECTTIME"`
	LastHeardTime time.Time `xml:"LASTHEARDTIME"`
}

type Station struct {
	Call      string `xml:"CALLSIGN" json:"callsign"`
	Via       string `xml:"VIANODE" json:"via"`
	Module    string `xml:"ONMODULE" json:"module"`
	Peer      string `xml:"VIAPEER" json:"peer"`
	LastHeard string `xml:"LASTHEARDTIME" json:"lastHeard"`
	//LastHeard time.Time `xml:"LASTHEARDTIME json:"lastHeard""`
}

func main() {
	var wg sync.WaitGroup

	log.SetFlags(log.Ldate | log.Lmicroseconds)
	// last heard time
	lh := time.Now().UTC().Format(time.RFC3339)

	// Flag declarations.
	xmlFile := flag.String("xml_file", "/var/log/mrefd.xml", "Path to the XML file to watch")
	flag.Parse()

	// Load MQTT configuration from .env file.
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	// Create MQTT client options.
	opts := mqtt.NewClientOptions()
	broker := os.Getenv("MQTT_BROKER")
	if broker == "" {
		broker = "tcp://127.0.0.1:1883"
	}
	opts.AddBroker(broker)
	opts.SetClientID("m17ref-watcher")
	opts.SetKeepAlive(60)
	opts.SetConnectRetry(true)
	opts.SetConnectRetryInterval(5 * time.Second)

	// Create MQTT client.
	client := mqtt.NewClient(opts)
	if err != nil {
		log.Fatalf("Error creating MQTT client: %v", err)
	}

	// Connect to MQTT broker.
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		log.Fatalf("Error connecting to MQTT broker: %v", token.Error())
	}

	// Create file watcher.
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatalf("Error creating file watcher: %v", err)
	}

	// Add XML file to file watcher.
	if err := watcher.Add(*xmlFile); err != nil {
		log.Fatalf("Error adding XML file to file watcher: %v", err)
	}

	// Handle signals.
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

	// Handle file change events.
	wg.Add(1)
	go func() {
		var differences []Station
		defer wg.Done()
		debounce := time.NewTimer(500 * time.Millisecond)
		defer debounce.Stop()
		dbtStopped := false

		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				//log.Println("event:", event)
				if event.Op&fsnotify.Write == fsnotify.Write {
					//log.Println("modified file:", event.Name)
					if !dbtStopped && !debounce.Stop() {
						<-debounce.C
					}
					// try to wait until a while after writes stop happening
					debounce.Reset(500 * time.Millisecond)
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Println("watch error:", err)
			case <-debounce.C:
				dbtStopped = true
				debounce.Stop()
				// Read XML file.
				stations, err := readStations(*xmlFile)
				if err != nil {
					log.Printf("Error reading XML file: %v", err)
					continue
				}

				// Check for stations with lastheard > last reported time.
				differences, lh = newTx(stations, lh)
				log.Printf("new lastheard %s\n", lh)

				// Publish differences to MQTT broker in JSON.
				if len(differences) > 0 {
					for _, difference := range differences {
						/*
							payload, err := json.Marshal(difference)
							if err != nil {
								log.Printf("Error marshalling difference: %v", err)
								continue
							}
						*/
						mqttTopic := fmt.Sprintf("M17-NZD/%s/%s", difference.Module, strings.ReplaceAll(difference.Call, " ", "-"))
						if token := client.Publish(mqttTopic, 0, false, "ON"); token.Wait() && token.Error() != nil {
							log.Printf("Error publishing to MQTT broker: %v", token.Error())
						}
					}
				}
			case <-signalChan:
				watcher.Close()
				client.Disconnect(1000)
				os.Exit(0)
			}
		}
	}()

	// Keep the program running.
	wg.Wait()
}

// readStations reads the stations from the specified XML file.
func readStations(file string) ([]Station, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var reflector Reflector
	if err := xml.Unmarshal(data, &reflector); err != nil {
		return reflector.Stations, err
	}
	log.Printf("read %d stations\n", len(reflector.Stations))
	return reflector.Stations, nil
}

// newTx returns the stations with lastheard > last reported time.
func newTx(onair []Station, lh string) ([]Station, string) {
	var differences []Station
	maxLh := lh

	log.Printf("=== lh target %s\n", lh)
	for i := len(onair) - 1; i >= 0; i-- {
		if onair[i].LastHeard > lh {
			station := onair[i]
			// set the call to the string up to the first space
			station.Call = strings.Split(station.Call, " ")[0]
			fmt.Printf("Tx: %s mod: %s %s", station.Call, station.Module, station.LastHeard)
			fmt.Printf("%+v\n", station)
			differences = append(differences, station)
			if station.LastHeard > maxLh {
				maxLh = station.LastHeard
			}
		}
	}
	return differences, maxLh
}
