package main

import (
	"crypto/tls"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/eleksir/go-xmpp"
	"github.com/go-redis/redis/v8"
	log "github.com/sirupsen/logrus"
	"gopkg.in/tomb.v2"
)

func main() {
	log.SetFormatter(&log.TextFormatter{ //nolint:exhaustruct
		DisableQuote:           true,
		DisableLevelTruncation: false,
		DisableColors:          true,
		FullTimestamp:          true,
		TimestampFormat:        "2006-01-02 15:04:05",
	})

	// Добавим динамически формируемый путь до конфига в список возможных положений конфига в системе.
	executablePath, err := os.Executable()

	if err != nil {
		log.Errorf("unable to get current executable path: %s", err)

		os.Exit(1)
	}

	for {
		sigChan = make(chan os.Signal, 1)
		talk = new(xmpp.Client)
		options = new(xmpp.Options)

		configJSONPath := fmt.Sprintf("%s/data/config.json", filepath.Dir(executablePath))
		configLocations = append(configLocations, configJSONPath)

		if err := readConfig(); err != nil {
			log.Error(err)

			os.Exit(1)
		}

		// no panic
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
		case "trace":
			log.SetLevel(log.TraceLevel)
		default:
			log.SetLevel(log.InfoLevel)
		}

		// Откроем лог и скормим его логгеру
		if config.Log != "" {
			logfile, err := os.OpenFile(config.Log, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)

			if err != nil {
				log.Fatalf("Unable to open log file %s: %s", config.Log, err)
			}

			log.SetOutput(logfile)
		}

		myLogLevel := log.GetLevel()
		log.Warnf("Loglevel set to %v", myLogLevel)

		// Через tomb попробуем сделать выход горутинок управляемым.
		gTomb := tomb.Tomb{}

		// Иницализируем redis-клиента.
		redisClient = redis.NewClient(&redis.Options{ //nolint:exhaustruct
			Addr: fmt.Sprintf("%s:%d", config.Redis.Server, config.Redis.Port),
		})

		log.Debugf("Lazy connect() to redis at %s:%d", config.Redis.Server, config.Redis.Port)
		subscriber = redisClient.Subscribe(ctx, config.Redis.MyChannel)

		verboseClient := false

		if myLogLevel == log.TraceLevel {
			verboseClient = true
		}

		// github.com/mattn/go-xmpp пишет в stdio, нам этого не надо, ловим выхлоп его в logrus с уровнем trace.
		xmpp.DebugWriter = log.WithFields(log.Fields{"logger": "stdlib"}).WriterLevel(log.TraceLevel)

		// Хэндлер сигналов не надо трогать, он нужен для завершения программы целиком.
		gTomb.Go(func() error { return sigHandler() })
		signal.Notify(sigChan, os.Interrupt)

		gTomb.Go(func() error { return redisLoop(subscriber.Channel()) })

		options = &xmpp.Options{ //nolint:exhaustruct
			Host:     fmt.Sprintf("%s:%d", config.Jabber.Server, config.Jabber.Port),
			User:     config.Jabber.User,
			Password: config.Jabber.Password,
			Resource: config.Jabber.Resource,
			NoTLS:    !config.Jabber.Ssl,
			StartTLS: config.Jabber.StartTLS,
			TLSConfig: &tls.Config{ //nolint:exhaustruct
				ServerName:         config.Jabber.Server,
				InsecureSkipVerify: !config.Jabber.SslVerify, //nolint:gosec
			},
			InsecureAllowUnencryptedAuth: config.Jabber.InsecureAllowUnencryptedAuth,
			Debug:                        verboseClient,
			Session:                      false,
			Status:                       "xa",
			StatusMessage:                randomPhrase(config.Jabber.StartupStatus),
			DialTimeout:                  time.Duration(config.Jabber.ConnectionTimeout) * time.Second,
		}

		// Устанавливаем соединение и гребём события, посылаемые сервером.
		gTomb.Go(func() error { return myLoop() })

		// Ловим первый же kill и не дождаемся остальных, хотя формально надо бы.
		_ = <-gTomb.Dying()

		time.Sleep(time.Duration(config.Jabber.ReconnectDelay) * time.Second)
	}
}

func myLoop() error {
	for {
		// Зададим начальное значение глобальным переменным.
		serverPingTimestampRx = 0
		serverPingTimestampTx = 0
		roomsConnected = make([]string, 1)
		lastActivity = 0
		lastServerActivity = 0
		lastMucActivity = NewCollection()
		serverCapsQueried = false
		serverCapsList = NewCollection()
		mucCapsList = NewCollection()
		serverPingTimestampTx = 0
		serverPingTimestampRx = 0
		roomPresences = NewCollection()

		// Установим коннект.
		if err := establishConnection(); err != nil {
			log.Error(err)

			return err
		}

		serverPingTimestampRx = time.Now().Unix() // Считаем, что если коннект запустился, то первый пинг успешен.

		// Тыкаем сервер палочкой, проверяем, что коннект жив и вываливаемся из mainLoop, если он не жив.
		gTomb.Go(
			func() error {
				err := probeServerLiveness()
				log.Error(err)
				return err
			},
		)

		// Тыкаем muc-и палочкой, проверяем, что они живы и пере-заходим в них, если пинги пропали.
		// Если пинги до комнаты пропали, то это фактически значит, что либо сервер потерял связь с MUC-компонентом,
		// либо у нас какой-то wire error.
		// gTomb.Go() тут зовётся внутри на каждую комнату, если у нас ошибка, эта горутинка рано или поздно выйдет.
		go probeMUCLiveness()

		// Гребём сообщения...
		for {
			// Стриггерилось завершение работы приложения, или соединение не установлено (порвалось, например).
			// грести не надо.
			if shutdown {
				break
			}

			if !isConnected {
				// Tight loop - это наверно не очень хорошо, думаю, ничего страшного не будет, если мы поспим 100мс.
				time.Sleep(100 * time.Millisecond)

				continue
			}

			// Вынимаем ивент из "провода".
			chat, err := talk.Recv()

			if err != nil {
				log.Errorf("Unable to get recieve data from socket: %s", err)

				return err
			}

			parseEvent(chat)
		}

		return nil
	}
}

/* vim: set ft=go noet ai ts=4 sw=4 sts=4: */
