package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/hjson/hjson-go"
	log "github.com/sirupsen/logrus"
)

// Конфиг
type myConfig struct {
	Server          string `json:"server,omitempty"`
	Port            int    `json:"port,omitempty"`
	Loglevel        string `json:"loglevel,omitempty"`
	Log             string `json:"log,omitempty"`
	Channel         string `json:"channel,omitempty"`
	ForwardChannels struct {
		Games   string `json:"games,omitempty"`
		Phrases string `json:"phrases,omitempty"`
		Webapp  string `json:"webapp,omitempty"`
		Craniac string `json:"craniac,omitempty"`
	} `json:"forward_channels"`
	Csign       string `json:"csign,omitempty"`
	ForwardsMax int64  `json:"forwards_max,omitempty"`
}

// Входящее сообщение из pubsub-канала redis-ки
type rMsg struct {
	From    string `json:"from,omitempty"`
	Chatid  string `json:"chatid,omitempty"`
	Userid  string `json:"userid,omitempty"`
	Message string `json:"message,omitempty"`
	Plugin  string `json:"plugin,omitempty"`
	Mode    string `json:"mode,omitempty"`
	Misc    struct {
		Answer      int64  `json:"answer,omitempty"`
		Botnick     string `json:"bot_nick,omitempty"`
		Csign       string `json:"csign,omitempty"`
		Fwdcnt      int64  `json:"fwd_cnt,omitempty"`
		GoodMorning int64  `json:"good_morning,omitempty"`
		Msgformat   int64  `json:"msg_format,omitempty"`
		Username    string `json:"username,omitempty"`
	} `json:"Misc"`
}

// Исходящее сообщение в pubsub-канал redis-ки
type sMsg struct {
	From    string `json:"from"`
	Chatid  string `json:"chatid"`
	Userid  string `json:"userid"`
	Message string `json:"message"`
	Plugin  string `json:"plugin"`
	Mode    string `json:"mode"`
	Misc    struct {
		Answer      int64  `json:"answer"`
		Botnick     string `json:"bot_nick"`
		Csign       string `json:"csign"`
		Fwdcnt      int64  `json:"fwd_cnt"`
		GoodMorning int64  `json:"good_morning"`
		Msgformat   int64  `json:"msg_format"`
		Username    string `json:"username"`
	} `json:"misc"`
}

// Config - это у нас глобальная штука :)
var config myConfig

// To break circular message forwarding we must set some sane default, it can be overridden via config
var forwardMax int64 = 5

// Объектики клиента-редиски
var redisClient *redis.Client
var subscriber *redis.PubSub

// Main context
var ctx = context.Background()

// Ставится в true, если мы получили сигнал на выключение
var shutdown = false

// Канал, в который приходят уведомления для хэндлера сигналов от траппера сигналов
var sigChan = make(chan os.Signal, 1)

// Горутинка, которая парсит json-чики прилетевшие из REDIS-ки
func msgParser(ctx context.Context, msg string) {
	var sendTo = "craniac"
	var j rMsg

	log.Debugf("Incomming raw json: %s", msg)

	if err := json.Unmarshal([]byte(msg), &j); err != nil {
		log.Warnf("Unable to to parse message from redis channel: %s", err)
		return
	}

	// Validate our j
	if exist := j.From; exist == "" {
		log.Warnf("Incorrect msg from redis, no from field: %s", msg)
		return
	}

	if exist := j.Chatid; exist == "" {
		log.Warnf("Incorrect msg from redis, no chatid field: %s", msg)
		return
	}

	if exist := j.Userid; exist == "" {
		log.Warnf("Incorrect msg from redis, no userid field: %s", msg)
		return
	}

	if exist := j.Message; exist == "" {
		log.Warnf("Incorrect msg from redis, no message field: %s", msg)
		return
	}

	if exist := j.Plugin; exist == "" {
		log.Warnf("Incorrect msg from redis, no plugin field: %s", msg)
		return
	}

	if exist := j.Mode; exist == "" {
		log.Warnf("Incorrect msg from redis, no mode field: %s", msg)
		return
	}

	// j.Misc.Answer может и не быть, тогда ответа на такое сообщение не будет
	// j.Misc.Botnick тоже можно не передавать, тогда будет записана пустая строка
	// j.Misc.Csign если нам его не передали, возьмём значение из конфига
	if exist := j.Misc.Csign; exist == "" {
		j.Misc.Csign = config.Csign
	}

	// j.Misc.Fwdcnt если нам его не передали, то будет 0
	if exist := j.Misc.Fwdcnt; exist == 0 {
		j.Misc.Fwdcnt = 1
	}

	// j.Misc.GoodMorning может быть быть 1 или 0, по-умолчанию 0
	// j.Misc.Msgformat может быть быть 1 или 0, по-умолчанию 0
	// j.Misc.Username можно не передавать, тогда будет пустая строка

	// Отвалидировались, теперь вернёмся к нашим баранам.

	// Если у нас циклическая пересылка сообщения, попробуем её тут разорвать, отбросив сообщение
	if j.Misc.Fwdcnt > config.ForwardsMax {
		log.Warnf("Discarding msg with fwd_cnt exceeding max value: %s", msg)
		return
	} else {
		j.Misc.Fwdcnt++
	}

	// Классифицирем входящие сообщения. Первым делом, попробуем определить команды
	if j.Message[0:len(j.Misc.Csign)] == j.Misc.Csign {
		// Может быть, это команда модуля phrases?
		var done = false
		var cmd = j.Message[len(j.Misc.Csign):]
		cmds := []string{"friday", "пятница", "proverb", "пословица", "fortune", "фортунка", "f", "ф", "karma", "карма"}

		for _, command := range cmds {
			if cmd == command {
				sendTo = "phrases"

				// Костыль для кармы
				if cmd == "karma" || cmd == "карма" {
					j.Misc.Answer = 1
				}

				done = true
				break
			}
		}

		// Не угадали? акей, как насчёт модуля webapp?
		if !done {
			cmds = []string{"buni", "anek", "анек", "анекдот", "cat", "кис", "drink", "праздник", "fox", "лис", "frog",
				"лягушка", "horse", "лошадь", "лошадка", "monkeyuser", "rabbit", "bunny", "кролик", "snail", "улитка",
				"owl", "сова", "сыч", "xkcd", "tits", "boobs", "tities", "boobies", "сиси", "сисечки", "butt", "booty",
				"ass", "попа", "попка"}

			for _, command := range cmds {
				if cmd == command {
					sendTo = "webapp"
					done = true
					break
				}
			}
		}

		// Не фортануло? может, повезёт с модулем games?
		if !done {
			cmds = []string{"dig", "копать", "fish", "fishing", "рыба", "рыбка", "рыбалка"}

			for _, command := range cmds {
				if cmd == command {
					sendTo = "games"
					done = true
					break
				}
			}
		}

		// Нет? Вдруг это комплексная команда?
		if !done {
			cmdLen := len(cmd)

			cmds := []string{"w ", "п ", "погода ", "погодка ", "погадка ", "weather ", "karma ", "карма "}

			for _, command := range cmds {
				if cmdLen > len(command) && cmd[0:len(command)] == command {
					if command == "karma " || command == "карма " {
						sendTo = "phrases"

						// Костыль для кармы
						j.Misc.Answer = 1
					} else {
						sendTo = "webapp"
					}

					break
				}
			}
		}
	} else {
		// Попробуем выискать изменение кармы
		msgLen := len(j.Message)

		// ++ или -- на конце фразы - это наш кандидат
		if msgLen > len("++") {
			if j.Message[msgLen-len("--"):msgLen] == "--" || j.Message[msgLen-len("++"):msgLen] == "++" {
				// Предполагается, что менять карму мы будем для одной фразы, то есть для 1 строки
				if len(strings.Split(j.Message, "\n")) == 1 {
					sendTo = "phrases"

					// Костыль для кармы
					j.Misc.Answer = 1
				}
			}
		}
	}

	// Настало время формировать json и засылать его в дальше
	var message sMsg
	message.From = j.From
	message.Userid = j.Userid
	message.Chatid = j.Chatid
	message.Message = j.Message
	message.Plugin = j.Plugin
	message.Mode = j.Mode
	message.Misc.Fwdcnt = j.Misc.Fwdcnt
	message.Misc.Csign = j.Misc.Csign
	message.Misc.Username = j.Misc.Username
	message.Misc.Answer = j.Misc.Answer
	message.Misc.Botnick = j.Misc.Botnick
	message.Misc.Msgformat = j.Misc.Msgformat
	message.Misc.GoodMorning = j.Misc.GoodMorning

	data, err := json.Marshal(message)

	if err != nil {
		log.Warnf("Unable to to serialize message for redis: %s", err)
		return
	}

	// Заталкиваем наш json в редиску
	if err := redisClient.Publish(ctx, sendTo, data).Err(); err != nil {
		log.Warnf("Unable to send data to redis channel %s: %s", sendTo, err)
	} else {
		log.Debugf("Send msg to redis channel %s: %s", sendTo, string(data))
	}
}

// Читает и валидирует конфиг, а также выставляет некоторые default-ы, если значений для параметров в конфиге нет
func readConfig() {
	configLoaded := false
	executablePath, err := os.Executable()

	if err != nil {
		log.Errorf("Unable to get current executable path: %s", err)
	}

	configJSONPath := fmt.Sprintf("%s/data/config.json", filepath.Dir(executablePath))

	locations := []string{
		"~/.aleesa-misc-go.json",
		"~/aleesa-misc-go.json",
		"/etc/aleesa-misc-go.json",
		configJSONPath,
	}

	for _, location := range locations {
		fileInfo, err := os.Stat(location)

		// Предполагаем, что файла либо нет, либо мы не можем его прочитать, второе надо бы логгировать, но пока забьём
		if err != nil {
			continue
		}

		// Конфиг-файл длинноват для конфига, попробуем следующего кандидата
		if fileInfo.Size() > 65535 {
			log.Warnf("Config file %s is too long for config, skipping", location)
			continue
		}

		buf, err := ioutil.ReadFile(location)

		// Не удалось прочитать, попробуем следующего кандидата
		if err != nil {
			log.Warnf("Skip reading config file %s: %s", location, err)
			continue
		}

		// Исходя из документации, hjson какбы умеет парсить "кривой" json, но парсит его в map-ку.
		// Интереснее на выходе получить структурку: то есть мы вначале конфиг преобразуем в map-ку, затем эту map-ку
		// сериализуем в json, а потом json преврщааем в стркутурку. Не очень эффективно, но он и не часто требуется.
		var sampleConfig myConfig
		var tmp map[string]interface{}
		err = hjson.Unmarshal(buf, &tmp)

		// Не удалось распарсить - попробуем следующего кандидата
		if err != nil {
			log.Warnf("Skip parsing config file %s: %s", location, err)
			continue
		}

		tmpjson, err := json.Marshal(tmp)

		// Не удалось преобразовать map-ку в json
		if err != nil {
			log.Warnf("Skip parsing config file %s: %s", location, err)
			continue
		}

		if err := json.Unmarshal(tmpjson, &sampleConfig); err != nil {
			log.Warnf("Skip parsing config file %s: %s", location, err)
			continue
		}

		// Валидируем значения из конфига
		if sampleConfig.Server == "" {
			sampleConfig.Server = "localhost"
		}

		if sampleConfig.Port == 0 {
			sampleConfig.Port = 6379
		}

		if sampleConfig.Loglevel == "" {
			sampleConfig.Loglevel = "info"
		}

		// sampleConfig.Log = "" if not set

		if sampleConfig.Channel == "" {
			log.Errorf("Channel field in config file %s must be set", location)
		}

		// Частичная проверка, ровно то, куда мы _точно_ щепрввляем сообщения исходя из бизнес-логики приложения
		if sampleConfig.ForwardChannels.Games == "" {
			sampleConfig.ForwardChannels.Games = "games"
		}

		if sampleConfig.ForwardChannels.Phrases == "" {
			sampleConfig.ForwardChannels.Phrases = "phrases"
		}

		if sampleConfig.ForwardChannels.Webapp == "" {
			sampleConfig.ForwardChannels.Webapp = "webapp"
		}

		if sampleConfig.ForwardChannels.Craniac == "" {
			sampleConfig.ForwardChannels.Craniac = "craniac"
		}

		if sampleConfig.Csign == "" {
			log.Errorf("Csign field in config file %s must be set", location)
		}

		if sampleConfig.ForwardsMax == 0 {
			sampleConfig.ForwardsMax = forwardMax
		}

		config = sampleConfig
		configLoaded = true
		log.Infof("Using %s as config file", location)
		break
	}

	if !configLoaded {
		log.Error("Config was not loaded! Refusing to start.")
	}
}

// Хэндлер сигналов закрывает все бд и сваливает из приложения
func sigHandler() {
	var err error

	for {
		var s = <-sigChan
		switch s {
		case syscall.SIGINT:
			log.Infoln("Got SIGINT, quitting")
		case syscall.SIGTERM:
			log.Infoln("Got SIGTERM, quitting")
		case syscall.SIGQUIT:
			log.Infoln("Got SIGQUIT, quitting")

		// Заходим на новую итерацию, если у нас "неинтересный" сигнал
		default:
			continue
		}

		// Чтобы не срать в логи ошибками от редиски, проставим shutdown state приложения в true
		shutdown = true

		// Отпишемся от всех каналов и закроем коннект к редиске
		if err = subscriber.Unsubscribe(ctx); err != nil {
			log.Errorf("Unable to unsubscribe from redis channels cleanly: %s", err)
		}

		if err = subscriber.Close(); err != nil {
			log.Errorf("Unable to close redis connection cleanly: %s", err)
		}

		os.Exit(0)
	}
}

// Производит некоторую инициализацию перед запуском main()
func init() {
	log.SetFormatter(&log.TextFormatter{
		DisableQuote:           true,
		DisableLevelTruncation: false,
		DisableColors:          true,
		FullTimestamp:          true,
		TimestampFormat:        "2006-01-02 15:04:05",
	})

	readConfig()

	// no panic, no trace
	switch config.Loglevel {
	case "fatal":
		log.SetLevel(log.FatalLevel)
	case "error":
		log.SetLevel(log.ErrorLevel)
	case "warn":
		log.SetLevel(log.WarnLevel)
	case "info":
		log.SetLevel(log.InfoLevel)
	case "debug":
		log.SetLevel(log.DebugLevel)
	default:
		log.SetLevel(log.InfoLevel)
	}
}

// Основная функция программы, не добавить и не убавить
func main() {
	// Main context
	var ctx = context.Background()

	// Откроем лог и скормим его логгеру
	if config.Log != "" {
		logfile, err := os.OpenFile(config.Log, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)

		if err != nil {
			log.Fatalf("Unable to open log file %s: %s", config.Log, err)
		}

		log.SetOutput(logfile)
	}

	// Иницализируем клиента
	redisClient = redis.NewClient(&redis.Options{
		Addr: fmt.Sprintf("%s:%d", config.Server, config.Port),
	})

	log.Debugf("Lazy connect() to redis at %s:%d", config.Server, config.Port)
	subscriber = redisClient.Subscribe(ctx, config.Channel)

	// Самое время поставить траппер сигналов
	signal.Notify(sigChan,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)

	go sigHandler()

	// Обработчик событий от редиски
	for {
		if shutdown {
			time.Sleep(1 * time.Second)
			continue
		}

		msg, err := subscriber.ReceiveMessage(ctx)

		if err != nil {
			if !shutdown {
				log.Warnf("Unable to connect to redis at %s:%d: %s", config.Server, config.Port, err)
			}

			time.Sleep(1 * time.Second)
			continue
		}

		go msgParser(ctx, msg.Payload)
	}
}
