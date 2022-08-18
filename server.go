package main

import (
	"encoding/json"
	"fmt"
	"github.com/dchest/uniuri"
	"github.com/gempir/go-twitch-irc/v3"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var version = "0.0.1"

// this maps the access keys for clients to the twitch channel they are associated with.
var keys = make(map[string]string)
var upgrader = websocket.Upgrader{} // use default options

var templates *template.Template

func NewLogger() (*zap.Logger, error) {
	cfg := zap.NewProductionConfig()
	cfg.OutputPaths = []string{
		"./log.log",
	}
	return cfg.Build()
}

type Config struct {
	Name           string `json:"name"`
	Contact        string `json:"contact"`
	AdminKey       string `json:"admin_key"`
	TwitchUsername string `json:"twitch_username"`
	TwitchCode     string `json:"twitch_code"`
	Host           string `json:"host"`
	Port           string `json:"port"`
	URL            string `json:"url"`
}

func loadConfig(sugar *zap.SugaredLogger) Config {
	file, err := os.ReadFile("config.json")
	if err != nil {
		if os.IsNotExist(err) {
			sugar.Fatal("You need to create the config.json from confg.json.example")
		}
		sugar.Fatal(err)
	}
	config := Config{}
	err = json.Unmarshal(file, &config)
	if err != nil {
		sugar.Fatal(err)
	}
	if config.AdminKey == "AnExposedKey" {
		sugar.Fatal("Admin key is not changed, please change it in config.json")
	}
	return config
}

type Client struct {
	ID   string
	Conn *websocket.Conn
	Pool *Pool
}

type Error struct {
	Error string `json:"error"`
}

func (c *Client) Read() (result error) {
	defer func() {
		c.Pool.Unregister <- c
		err := c.Conn.Close()
		if err != nil {
			result = err
		}
		result = nil
	}()

	for {
		_, p, err := c.Conn.ReadMessage()
		if err != nil {
			return err
		}
		if string(p) == "ping" {
			err := c.Conn.WriteJSON(struct {
				Pong string `json:"pong"`
			}{Pong: "pong"})
			if err != nil {
				return err
			}
		} else {
			replyID, messageToSend, _ := strings.Cut(string(p), " ")
			c.Pool.TwitchClient.Reply(c.ID, replyID, messageToSend)
		}
	}
}

type Pool struct {
	Register     chan *Client
	Unregister   chan *Client
	Clients      map[string]*Client
	TwitchClient *twitch.Client
}

func (pool *Pool) Start() {
	for {
		select {
		case client := <-pool.Register:
			pool.Clients[client.ID] = client
			err := pool.SendMessage(client.ID, struct {
				Version string `json:"version"`
			}{Version: version})
			if err != nil {
				return
			}
			break
		case client := <-pool.Unregister:
			delete(pool.Clients, client.ID)
			pool.TwitchClient.Depart(client.ID)
			break
		}
	}
}

func (pool *Pool) SendMessage(clientID string, json interface{}) error {
	err := pool.Clients[clientID].Conn.WriteJSON(json)
	if err != nil {
		return err
	}
	return nil
}

func (pool *Pool) SendError(clientID string, body string) error {
	err := pool.Clients[clientID].Conn.WriteJSON(Error{Error: body})
	if err != nil {
		return err
	}
	return nil
}

func NewPool(twitchClient *twitch.Client) *Pool {
	return &Pool{
		Register:     make(chan *Client),
		Unregister:   make(chan *Client),
		Clients:      make(map[string]*Client),
		TwitchClient: twitchClient,
	}
}

func loadKeys(sugar *zap.SugaredLogger) {
	file, err := os.ReadFile("keys.json")
	if err != nil {
		if os.IsNotExist(err) {
			sugar.Info("No keys.json found, creating new one")
			tempFile, err := os.Create("keys.json")
			if err != nil {
				sugar.Fatal(err)
			}
			_, err = tempFile.WriteString(`{}`)
			if err != nil {
				sugar.Fatal(err)
			}
			err = tempFile.Close()
			if err != nil {
				sugar.Fatal(err)
			}
			return
		} else {
			sugar.Fatal(err)
		}
		sugar.Fatal(err)
	}
	err = json.Unmarshal(file, &keys)
	if err != nil {
		sugar.Fatal(err)
	}
}

var saveKeys = func(sugar *zap.SugaredLogger) {
	jsonString, err := json.MarshalIndent(keys, "", "   ")
	if err != nil {
		sugar.Error(err)
	}
	err = os.WriteFile("keys.json", jsonString, 0644)
	if err != nil {
		sugar.Error(err)
	}
}

func loadTemplates(sugar *zap.SugaredLogger) {
	var err error
	templates, err = template.ParseFiles([]string{filepath.Join("htmlFiles", "index.html"), filepath.Join("htmlFiles", "admin.html"), filepath.Join("htmlFiles", "tableEntryTemplate.html")}...)
	if err != nil {
		sugar.Fatal(err)
	}
}

func loadHandlers(sugar *zap.SugaredLogger, config Config, pool *Pool, twitchClient *twitch.Client) {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		index(w, r, sugar, config)
	})
	http.HandleFunc("/style.css", style)
	http.HandleFunc("/script.js", javascript)
	http.HandleFunc("/addClient", func(w http.ResponseWriter, r *http.Request) {
		addClient(w, r, sugar, config)
	})
	http.HandleFunc("/removeClients", func(w http.ResponseWriter, r *http.Request) {
		removeClients(w, r, sugar, config)
	})
	http.HandleFunc("/setup", func(w http.ResponseWriter, r *http.Request) {
		setup(w, r, sugar)
	})
	http.HandleFunc("/websocket", func(w http.ResponseWriter, r *http.Request) {
		websocketHandler(w, r, sugar, pool, twitchClient)
	})
	http.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		admin(w, r, sugar, config, pool)
	})
}

func serveErrorFile(w http.ResponseWriter, name string, statusCode int) error {
	file, err := os.ReadFile(filepath.Join("htmlFiles", name))
	if err != nil {
		return err
	}
	w.WriteHeader(statusCode)
	_, err = w.Write(file)
	if err != nil {
		return err
	}
	return nil
}

func serveErrorJson(w http.ResponseWriter, jsonToServe any, statusCode int) error {
	j, _ := json.Marshal(jsonToServe)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_, err := w.Write(j)
	if err != nil {
		return err
	}
	return nil
}

func main() {
	logger, err := NewLogger()
	if err != nil {
		panic(err.(any))
	}
	defer func(logger *zap.Logger) {
		err := logger.Sync()
		if err != nil {
			panic(err.(any))
		}
	}(logger)
	sugar := logger.Sugar()
	config := loadConfig(sugar)
	loadKeys(sugar)
	loadTemplates(sugar)
	twitchClient := twitch.NewClient(config.TwitchUsername, "oauth:"+config.TwitchCode)

	pool := NewPool(twitchClient)
	go pool.Start()
	loadHandlers(sugar, config, pool, twitchClient)

	twitchClient.OnPrivateMessage(func(message twitch.PrivateMessage) {
		fmt.Println("Message from twitch: " + message.Message)
		err := pool.SendMessage(strings.ToLower(message.Channel), message)
		if err != nil {
			sugar.Error(err)
		}
	})
	sugar.Info("Starting server")
	twitchClient.OnUserNoticeMessage(func(message twitch.UserNoticeMessage) {
		sugar.Info(message)
		sugar.Info("Message from twitch: " + message.Message)
		err := pool.SendMessage(strings.ToLower(message.Channel), message)
		if err != nil {
			sugar.Error(err)
		}
	})

	go func() {
		for {
			err := twitchClient.Connect()
			if err != nil {
				// this is apparently the error when there is a connection error, so we retry after like 5 seconds
				if err.Error() == "dial tcp: lookup irc.chat.twitch.tv: no such host" {
					sugar.Info("Connection error, retrying in 5 seconds")
					time.Sleep(5 * time.Second)
				} else {
					sugar.Error(err)
				}
			}
		}
	}()
	sugar.Fatal(http.ListenAndServe(config.Host+":"+config.Port, nil))
}

// called by browser
func style(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, filepath.Join("htmlFiles", "style.css"))
}

func javascript(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, filepath.Join("htmlFiles", "script.js"))
}

func index(w http.ResponseWriter, _ *http.Request, sugar *zap.SugaredLogger, config Config) {
	// thanks, DigitalOcean, for teaching inline structs
	// https://www.digitalocean.com/community/tutorials/defining-structs-in-go
	p := struct {
		Name    string
		Contact template.HTML
	}{
		Name:    config.Name,
		Contact: template.HTML(config.Contact),
	}
	err := templates.ExecuteTemplate(w, "index.html", p)
	if err != nil {
		sugar.Error(err)
	}
}

func addClient(w http.ResponseWriter, r *http.Request, sugar *zap.SugaredLogger, config Config) {
	adminKey, ok := r.URL.Query()["admin_key"]
	if !(ok && len(adminKey[0]) > 1 && adminKey[0] == config.AdminKey) {
		var err error
		if ok {
			sugar.Infof("The admin key %s was expected, %s was supplied", config.AdminKey, adminKey[0])
			err = serveErrorJson(w, errorJson{ErrorCode: http.StatusUnauthorized, Description: "Wrong admin key was supplied."}, http.StatusUnauthorized)
		} else {
			sugar.Infof("No admin key was supplied")
			err = serveErrorJson(w, errorJson{ErrorCode: http.StatusBadRequest, Description: "No admin key was supplied."}, http.StatusUnauthorized)
		}
		if err != nil {
			sugar.Error(err)
		}
		return
	}
	twitchChannel, ok := r.URL.Query()["twitch_channel"]
	if !(ok) {
		err := serveErrorJson(w, errorJson{ErrorCode: http.StatusBadRequest, Description: "No twitch channel was supplied."}, http.StatusBadRequest)
		if err != nil {
			sugar.Error(err)
		}
		return
	}
	runLoop := true
	var clientKey string
	for runLoop {
		clientKey = uniuri.NewLen(10)
		if _, ok := keys[clientKey]; !ok {
			runLoop = false
		}
	}
	keys[clientKey] = twitchChannel[0]
	saveKeys(sugar)
	w.Header().Set("Content-Type", "application/json")
	_, err := w.Write([]byte("{}"))
	if err != nil {
		sugar.Error(err)
	}
}

func removeClients(w http.ResponseWriter, r *http.Request, sugar *zap.SugaredLogger, config Config) {
	adminKey, ok := r.URL.Query()["admin_key"]
	if !(ok && len(adminKey[0]) > 1 && adminKey[0] == config.AdminKey) {
		var err error
		if ok {
			sugar.Infof("The admin key %s was expected, %s was supplied", config.AdminKey, adminKey[0])
			err = serveErrorJson(w, errorJson{ErrorCode: http.StatusUnauthorized, Description: "Wrong admin key was supplied."}, http.StatusUnauthorized)
		} else {
			sugar.Infof("No admin key was supplied")
			err = serveErrorJson(w, errorJson{ErrorCode: http.StatusBadRequest, Description: "No admin key was supplied."}, http.StatusBadRequest)
		}
		if err != nil {
			sugar.Error(err)
		}
		return
	}
	clientKey, ok := r.URL.Query()["clients"]
	if !(ok && len(clientKey[0]) > 1) {
		err := serveErrorJson(w, errorJson{ErrorCode: http.StatusBadRequest, Description: "No client key was supplied."}, http.StatusBadRequest)
		if err != nil {
			sugar.Error(err)
		}
		return
	}
	for _, k := range clientKey {
		_, ok = keys[k]
		if !ok {
			err := serveErrorJson(w, errorJson{ErrorCode: http.StatusBadRequest, Description: "The client key " + k + " does not exist, deletion aborted."}, http.StatusBadRequest)
			if err != nil {
				sugar.Error(err)
			}
			return
		}
	}
	for _, k := range clientKey {
		delete(keys, k)
	}
	saveKeys(sugar)
	w.Header().Set("Content-Type", "application/json")
	_, err := w.Write([]byte("{}"))
	if err != nil {
		sugar.Error(err)
	}
}

func admin(w http.ResponseWriter, r *http.Request, sugar *zap.SugaredLogger, config Config, pool *Pool) {
	adminKey, ok := r.URL.Query()["admin_key"]
	if !(ok && len(adminKey[0]) > 1 && adminKey[0] == config.AdminKey) {
		var err error
		if ok {
			sugar.Infof("The admin key %s was expected, %s was supplied", config.AdminKey, adminKey[0])
			err = serveErrorFile(w, "wrongAdminKey.html", http.StatusUnauthorized)
		} else {
			sugar.Infof("No admin key was supplied")
			err = serveErrorFile(w, "noAdminKey.html", http.StatusBadRequest)
		}
		if err != nil {
			sugar.Error(err)
		}
		return
	}
	var channelsConnected []template.HTML
	var channelsUnconnected []template.HTML
	type p struct {
		ID        string
		Login     string
		Connected string
	}
	for id, login := range keys {
		b := new(strings.Builder)
		if _, ok := pool.Clients[strings.ToLower(login)]; ok {
			err := templates.ExecuteTemplate(b, "tableEntryTemplate.html", p{ID: id, Login: login, Connected: "✅"})
			if err != nil {
				sugar.Error(err)
			}
			channelsConnected = append(channelsConnected, template.HTML(b.String()))
		} else {
			err := templates.ExecuteTemplate(b, "tableEntryTemplate.html", p{ID: id, Login: login, Connected: "❌"})
			if err != nil {
				sugar.Error(err)
			}
			channelsUnconnected = append(channelsUnconnected, template.HTML(b.String()))
		}
	}
	err := templates.ExecuteTemplate(w, "admin.html", struct{ Channels []template.HTML }{Channels: append(channelsConnected, channelsUnconnected...)})
	if err != nil {
		sugar.Error(err)
	}
}

type errorJson struct {
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
}

// called by clients

func setup(w http.ResponseWriter, r *http.Request, sugar *zap.SugaredLogger) {
	clientKey, ok := r.URL.Query()["client_key"]
	if !(ok && len(clientKey[0]) > 1) {
		err := serveErrorJson(w, errorJson{ErrorCode: http.StatusBadRequest, Description: "No client key was supplied"}, http.StatusBadRequest)
		if err != nil {
			sugar.Error(err)
		}
		return
	}
	_, ok = keys[clientKey[0]]
	if !ok {
		err := serveErrorJson(w, errorJson{ErrorCode: http.StatusUnauthorized, Description: "This client key is wrong"}, http.StatusUnauthorized)
		if err != nil {
			sugar.Error(err)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	response, _ := json.Marshal(struct {
		Version string `json:"version"`
	}{Version: version})
	_, err := w.Write(response)
	if err != nil {
		sugar.Error(err)
	}
}

func websocketHandler(w http.ResponseWriter, r *http.Request, sugar *zap.SugaredLogger, pool *Pool, twitchClient *twitch.Client) {
	var authorized = true
	clientKey, ok := r.URL.Query()["client_key"]
	if !(ok && len(clientKey[0]) > 1) {
		authorized = false
	}
	if authorized {
		_, ok := keys[clientKey[0]]
		if !ok {
			authorized = false
		}
	}

	if !websocket.IsWebSocketUpgrade(r) {
		err := serveErrorJson(w, errorJson{ErrorCode: http.StatusBadRequest, Description: "This URL needs to be used for a websocket connection"}, http.StatusBadRequest)
		if err != nil {
			sugar.Error(err)
		}
		return
	}
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		sugar.Error(err)
		return
	}
	if !authorized {
		// this is not the correct error message if someone doesn't submit an error at all, but that's not our problem
		// now is it.
		err := c.WriteJSON(Error{Error: "The wrong authorization key submitted"})
		if err != nil {
			sugar.Error(err)
			return
		}
		time.Sleep(2 * time.Second)
		err = c.Close()
		if err != nil {
			sugar.Error(err)
			return
		}
		return
	}
	twitchClient.Join(strings.ToLower(keys[clientKey[0]]))
	client := &Client{
		Conn: c,
		Pool: pool,
		ID:   strings.ToLower(keys[clientKey[0]]),
	}
	pool.Register <- client
	err = client.Read()
	if err != nil {
		sugar.Error(err)
		return
	}
}
