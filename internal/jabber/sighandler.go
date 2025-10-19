package jabber

import (
	"aleesa-jabber-go/internal/log"
	"os"
	"syscall"

	"github.com/eleksir/go-xmpp"
)

// SigHandler Хэндлер сигналов закрывает все бд, все сетевые соединения и сваливает из приложения.
func SigHandler() error {
	log.Debug("Installing signal handler")

	for s := range SigChan {
		switch s {
		case syscall.SIGINT:
			log.Info("Got SIGINT, quitting")
		case syscall.SIGTERM:
			log.Info("Got SIGTERM, quitting")
		case syscall.SIGQUIT:
			log.Info("Got SIGQUIT, quitting")

		// Заходим на новую итерацию, если у нас "неинтересный" сигнал.
		default:
			continue
		}

		var err error

		// Чтобы не срать в логи ошибками, проставим shutdown state приложения в true.
		Shutdown = true

		// Отпишемся от всех каналов и закроем коннект к редиске
		if err = Subscriber.Unsubscribe(Ctx); err != nil {
			log.Errorf("Unable to unsubscribe from redis channels cleanly: %s", err)
		} else {
			log.Debug("Unsubscribe from all redis channels")
		}

		if err = Subscriber.Close(); err != nil {
			log.Errorf("Unable to close redis connection cleanly: %s", err)
		} else {
			log.Debug("Close redis connection")
		}

		if IsConnected && !Shutdown {
			log.Debug("Try to set our presence to Unavailable and status to Offline")

			// Вот тут понадобится коллекция известных пользователей, чтобы им разослать presence, что бот свалил в offline
			// Пока за неимением лучшего сообщим об этом самим себе.
			for _, room := range RoomsConnected {
				if _, err := Talk.SendPresence(
					xmpp.Presence{ //nolint:exhaustruct
						To:     room,
						Status: "Offline",
						Type:   "unavailable",
					},
				); err != nil {
					log.Infof("Unable to send presence to jabber server: %s", err)
				}
			}

			// И закрываем соединение.
			log.Info("Closing connection to jabber server")

			if err := Talk.Close(); err != nil {
				log.Infof("Unable to close connection to jabber server: %s", err)
			}
		}

		if len(settingsDB) > 0 {
			log.Debug("Closing runtime jabber room settings db")

			for _, db := range settingsDB {
				_ = db.Close()
			}
		}

		os.Exit(0)
	}

	return nil
}

/* vim: set ft=go noet ai ts=4 sw=4 sts=4: */
