package proxy

import (
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		origin := strings.ToLower(r.Header.Get("Origin"))
		Logger.Printf("client %s Origin=%s request websocket server", r.RemoteAddr, origin)
		if InStringArray("*", GConfig.Origins) || InStringArray(origin, GConfig.Origins) {
			return true
		}

		return false
	},
}

func faviconHttpHandle(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte{})
}

func defaultHttpHandle(w http.ResponseWriter, r *http.Request) {
	for {
		rv, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || rv.Get("format") != "json" {
			break
		}

		ret, err := json.Marshal(struct {
			Version string `json:"version"`
			Time    int64  `json:"time"`
			Total   int    `json:"total"`
		}{VERSION, time.Now().UnixNano() / 1e6, Clients.num})

		if err != nil {
			break
		}

		w.Header().Set("Content-Type", "text/json")
		w.Write(ret)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, "version: %s<br/>", VERSION)
	fmt.Fprintf(w, "time: %d<br/>", time.Now().Unix())
	fmt.Fprintf(w, "total: %d<br/>", Clients.num)
}

func sockHttpHandle(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		Logger.Print(err)
		return
	}

	defer conn.Close()

	rv, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		Logger.Print(err)
		return
	}

	clientUUID := rv.Get("uuid")
	if len(clientUUID) == 0 {
		clientUUID = uuid.New().String()
	}

	clientCategory := MessageToRequestClient
	if rv.Get("monitor") == "1" {
		clientCategory = MessageToMonitorClient
	}

	Logger.Printf("client %s[%s] connected with query[%s]", conn.RemoteAddr(), clientUUID, r.URL.RawQuery)

	client := Clients.GetClient(clientUUID)
	if client != nil {
		client.Close()
	}

	client = Clients.AddNewClient(clientCategory, clientUUID, conn, r, &rv)

	defer func() {
		Clients.RemoveClient(clientUUID)
		Logger.Printf("client %s[%s] disconnected", conn.RemoteAddr(), clientUUID)
	}()

	go client.PipeSendMessage()
	client.PipeReadMessage()
}

func logsHttpHandle(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.Header().Set("Allow", "POST")
		w.Header().Set("Cache-Control", "max-age=3600")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST")
		requestAllowHeaders := r.Header.Get("Access-Control-Request-Headers")
		if len(requestAllowHeaders) > 0 {
			w.Header().Set("Access-Control-Allow-Headers", requestAllowHeaders)
		}
		w.Header().Set("Access-Control-Max-Age", "3600")
		w.WriteHeader(204)
		return
	}

	var responseContent = "fail"
	defer func() {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Write([]byte(responseContent))
	}()

	if r.Method != http.MethodPost || r.ContentLength == 0 {
		return
	}

	rv, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		Logger.Print(err)
		return
	}

	message, err := ioutil.ReadAll(r.Body)
	if err != nil {
		Logger.Printf("read client %s post logs failed %s", r.RemoteAddr, err)
		return
	}

	if len(GConfig.LoggerRc4EncryptKey) != 0 {
		messagePlainText, err := Rc4Decrypt(message, []byte(GConfig.LoggerRc4EncryptKey))
		if err != nil {
			Logger.Printf("read client %s post logs to decrypt failed %s", r.RemoteAddr, err)
			return
		}
		message = messagePlainText
	}

	var logMessage LogMessage
	if err := json.Unmarshal(message, &logMessage); err != nil {
		Logger.Printf("client %s post error type logs content %s", r.RemoteAddr, message)
		return
	}

	remoteInfo := strings.Split(r.RemoteAddr, ":")

	qstr := GConfig.QueryString
	if len(qstr) > 0 {
		if len(r.URL.RawQuery) > 0 {
			qstr = fmt.Sprintf("%s&%s", qstr, r.URL.RawQuery)
		}
	} else {
		qstr = r.URL.RawQuery
	}

	pubSubMessage := NewPubSubMessage(rv.Get("uuid"), remoteInfo[0], remoteInfo[1], qstr, r.Header.Get("User-Agent"))
	pubSubMessage.UpdateMessage(PubSubMessageTypeIsLogs, logMessage)
	pubSubMessage.Durable()

	responseContent = "ok"
}

func pushHttpHandle(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.Header().Set("Allow", "POST")
		w.Header().Set("Cache-Control", "max-age=3600")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST")
		requestAllowHeaders := r.Header.Get("Access-Control-Request-Headers")
		if len(requestAllowHeaders) > 0 {
			w.Header().Set("Access-Control-Allow-Headers", requestAllowHeaders)
		}
		w.Header().Set("Access-Control-Max-Age", "3600")
		w.WriteHeader(204)
		return
	}

	var responseContent = "fail"
	defer func() {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Access-Control-Allow-Origin", "*");
		w.Write([]byte(responseContent))
	}()

	if r.Method != http.MethodPost || r.ContentLength == 0 {
		return
	}

	rv, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return
	}

	group := rv.Get("group")
	if group == "" {
		group = "*"
	}

	message, err := ioutil.ReadAll(r.Body)
	if err != nil {
		Logger.Printf("read client %s post message failed %s", r.RemoteAddr, err)
		return
	}

	messageData := &TPushMessageData{}
	err = json.Unmarshal(message, messageData)
	if err != nil {
		Logger.Printf("read client %s post message failed %s", r.RemoteAddr, err)
		return
	}

	go func() {
		//iOS
		QpushMessage(group, messageData.Message)
		//android
		GAndroidPushDevices.PushMessage(group, messageData)
		//monitor
		if group == "*" {
			Clients.BroadcastMessage(NewClientTextMessage(message), MessageToMonitorClient)
		} else {
			Clients.PushMessage(group, NewClientTextMessage(message), MessageToMonitorClient)
		}
	}()

	responseContent = "ok"
}

func NewWebSocket() (*http.Server, chan int) {
	http.HandleFunc("/", defaultHttpHandle)
	http.HandleFunc("/favicon.ico", faviconHttpHandle)
	http.HandleFunc("/sock", sockHttpHandle)
	http.HandleFunc("/push", pushHttpHandle)

	if len(GConfig.LoggerMysqlConfig.Ip) > 0 && len(GConfig.LoggerMysqlConfig.Username) > 0 {
		http.HandleFunc("/logs", logsHttpHandle)
	}

	if len(GConfig.HttpStaticRoot) > 0 {
		http.Handle("/res", http.StripPrefix("/res", http.FileServer(http.Dir(GConfig.HttpStaticRoot))))
	}

	httpServer := &http.Server{
		Addr:           GConfig.HttpServerAddress,
		Handler:        http.DefaultServeMux,
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   30 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	httpStop := make(chan int)

	go func() {
		Logger.Printf("http server will run at %s", GConfig.HttpServerAddress)

		LoggerMessageRecord.Run()

		var err error
		if len(GConfig.HttpServerSSLCert) > 0 && len(GConfig.HttpServerSSLKey) > 0 {
			err = httpServer.ListenAndServeTLS(GConfig.HttpServerSSLCert, GConfig.HttpServerSSLKey)
		} else {
			err = httpServer.ListenAndServe()
		}

		if err != nil && err != http.ErrServerClosed {
			Logger.Print(err)
		}
		Logger.Printf("http server stop at %s", GConfig.HttpServerAddress)
		httpStop <- 1
	}()

	return httpServer, httpStop
}
