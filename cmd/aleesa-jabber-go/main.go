package main

import (
	"crypto/tls"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"aleesa-jabber-go/internal/anycollection"
	"aleesa-jabber-go/internal/jabber"
	"aleesa-jabber-go/internal/log"

	"github.com/eleksir/go-xmpp"
	"github.com/go-redis/redis/v8"
	"gopkg.in/tomb.v2"
)

func main() {
	var (
		err           error
		logfile       *os.File
		logVebosity   string
		verboseClient = false
	)

	// Добавим динамически формируемый путь до конфига в список возможных положений конфига в системе.
	executablePath, err := os.Executable()

	if err != nil {
		log.Errorf("unable to get current executable path: %s", err)

		os.Exit(1)
	}

	configJSONPath := fmt.Sprintf("%s/data/config.json", filepath.Dir(executablePath))
	jabber.ConfigLocations = append(jabber.ConfigLocations, configJSONPath)

	if err := jabber.ReadConfig(); err != nil {
		log.Errorf("%s", err)

		os.Exit(1)
	}

	// Откроем лог и скормим его логгеру
	if jabber.Config.Log == "" {
		logfile = os.Stderr
	} else {
		logfile, err = os.OpenFile(jabber.Config.Log, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)

		if err != nil {
			log.Errorf("Unable to open log file %s: %s", jabber.Config.Log, err)
			os.Exit(1)
		}
	}

	log.Init(jabber.Config.Loglevel, logfile)
	logVebosity = log.GetLevel()
	log.Warnf("Loglevel set to %v", logVebosity)

	for {
		jabber.SigChan = make(chan os.Signal, 1)
		jabber.Talk = new(xmpp.Client)
		jabber.Options = new(xmpp.Options)

		// Через tomb попробуем сделать выход горутинок управляемым.
		jabber.GTomb = tomb.Tomb{}

		// Иницализируем redis-клиента.
		jabber.RedisClient = redis.NewClient(&redis.Options{ //nolint:exhaustruct
			Addr: fmt.Sprintf("%s:%d", jabber.Config.Redis.Server, jabber.Config.Redis.Port),
		})

		log.Debugf("Lazy connect() to redis at %s:%d", jabber.Config.Redis.Server, jabber.Config.Redis.Port)
		jabber.Subscriber = jabber.RedisClient.Subscribe(jabber.Ctx, jabber.Config.Redis.MyChannel)

		if logVebosity == "debug" {
			verboseClient = true
		}

		// github.com/mattn/go-xmpp пишет в stdio, нам этого не надо, ловим выхлоп его в logrus с уровнем trace.
		xmpp.DebugWriter = log.Writer

		// Хэндлер сигналов не надо трогать, он нужен для завершения программы целиком.
		jabber.GTomb.Go(jabber.SigHandler)
		signal.Notify(jabber.SigChan, os.Interrupt)

		jabber.GTomb.Go(func() error { return jabber.RedisLoop(jabber.Subscriber.Channel()) })

		jabber.Options = &xmpp.Options{ //nolint:exhaustruct
			Host:     fmt.Sprintf("%s:%d", jabber.Config.Jabber.Server, jabber.Config.Jabber.Port),
			User:     jabber.Config.Jabber.User,
			Password: jabber.Config.Jabber.Password,
			Resource: jabber.Config.Jabber.Resource,
			NoTLS:    !jabber.Config.Jabber.Ssl,
			StartTLS: jabber.Config.Jabber.StartTLS,
			TLSConfig: &tls.Config{ //nolint:exhaustruct
				ServerName:         jabber.Config.Jabber.Server,
				InsecureSkipVerify: !jabber.Config.Jabber.SslVerify, //nolint:gosec
			},
			InsecureAllowUnencryptedAuth: jabber.Config.Jabber.InsecureAllowUnencryptedAuth,
			Debug:                        verboseClient,
			Session:                      false,
			Status:                       "xa",
			StatusMessage:                jabber.RandomPhrase(jabber.Config.Jabber.StartupStatus),
			DialTimeout:                  time.Duration(jabber.Config.Jabber.ConnectionTimeout) * time.Second,
		}

		// Устанавливаем соединение и гребём события, посылаемые сервером.
		jabber.GTomb.Go(myLoop)

		// Ловим первый же kill и не дождаемся остальных, хотя формально надо бы.
		<-jabber.GTomb.Dying()

		time.Sleep(time.Duration(jabber.Config.Jabber.ReconnectDelay) * time.Second)
	}
}

func myLoop() error {
	for {
		// Зададим начальное значение глобальным переменным.
		jabber.ServerPingTimestampRx = 0
		jabber.ServerPingTimestampTx = 0
		jabber.RoomsConnected = make([]string, 1)
		jabber.LastActivity = 0
		jabber.LastServerActivity = 0
		jabber.LastMucActivity = anycollection.NewCollection()
		jabber.ServerCapsQueried = false
		jabber.ServerCapsList = anycollection.NewCollection()
		jabber.MucCapsList = anycollection.NewCollection()
		jabber.RoomPresences = anycollection.NewCollection()

		// Установим коннект.
		if err := jabber.EstablishConnection(); err != nil {
			log.Errorf("%s", err)

			return err
		}

		jabber.ServerPingTimestampRx = time.Now().Unix() // Считаем, что если коннект запустился, то первый пинг успешен.

		// Тыкаем сервер палочкой, проверяем, что коннект жив и вываливаемся из mainLoop, если он не жив.
		jabber.GTomb.Go(
			func() error {
				err := jabber.ProbeServerLiveness()
				log.Errorf("%s", err)

				return err
			},
		)

		// Тыкаем muc-и палочкой, проверяем, что они живы и пере-заходим в них, если пинги пропали.
		// Если пинги до комнаты пропали, то это фактически значит, что либо сервер потерял связь с MUC-компонентом,
		// либо у нас какой-то wire error.
		// GTomb.Go() тут зовётся внутри на каждую комнату, если у нас ошибка, эта горутинка рано или поздно выйдет.
		go jabber.ProbeMUCLiveness()

		// Гребём сообщения...
		for {
			// Стриггерилось завершение работы приложения, или соединение не установлено (порвалось, например).
			// грести не надо.
			if jabber.Shutdown {
				break
			}

			if !jabber.IsConnected {
				// Tight loop - это наверно не очень хорошо, думаю, ничего страшного не будет, если мы поспим 100мс.
				time.Sleep(100 * time.Millisecond)

				continue
			}

			// Вынимаем ивент из "провода".
			chat, err := jabber.Talk.Recv()

			if err != nil {
				log.Errorf("Unable to get recieve data from socket: %s", err)

				return err
			}

			jabber.ParseEvent(chat)
		}

		return nil
	}
}

/* vim: set ft=go noet ai ts=4 sw=4 sts=4: */
