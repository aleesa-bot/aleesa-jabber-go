package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"strings"
	"syscall"
	"time"

	"github.com/eleksir/go-xmpp"
	log "github.com/sirupsen/logrus"
	"golang.org/x/exp/slices"
)

// sigHandler Хэндлер сигналов закрывает все бд, все сетевые соединения и сваливает из приложения.
func sigHandler() {
	log.Debug("Installing signal handler")

	for s := range sigChan {
		switch s {
		case syscall.SIGINT:
			log.Infoln("Got SIGINT, quitting")
		case syscall.SIGTERM:
			log.Infoln("Got SIGTERM, quitting")
		case syscall.SIGQUIT:
			log.Infoln("Got SIGQUIT, quitting")

		// Заходим на новую итерацию, если у нас "неинтересный" сигнал.
		default:
			continue
		}

		var err error

		// Чтобы не срать в логи ошибками, проставим shutdown state приложения в true.
		shutdown = true

		// Отпишемся от всех каналов и закроем коннект к редиске
		if err = subscriber.Unsubscribe(ctx); err != nil {
			log.Errorf("Unable to unsubscribe from redis channels cleanly: %s", err)
		} else {
			log.Debug("Unsubscribe from all redis channels")
		}

		if err = subscriber.Close(); err != nil {
			log.Errorf("Unable to close redis connection cleanly: %s", err)
		} else {
			log.Debug("Close redis connection")
		}

		if isConnected && !shutdown {
			log.Debug("Try to set our presence to Unavailable and status to Offline")

			// Вот тут понадобится коллекция известных пользователей, чтобы им разослать presence, что бот свалил в offline
			// Пока за неимением лучшего сообщим об этом самим себе.
			for _, room := range roomsConnected {
				if _, err := talk.SendPresence(
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
			log.Debugf("Closing connection to jabber server")

			if err := talk.Close(); err != nil {
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
}

// establishConnection устанавливает соединение с jabber-сервером.
func establishConnection() {
	var err error

	if connecting && !isConnected {
		return
	}

	// Проставляем глобальные переменные.
	connecting = true
	isConnected = false
	roomsConnected = make([]string, 0)

	talk, err = options.NewClient()

	if err != nil {
		gTomb.Kill(err)

		return
	}

	// По идее keepalive должен же проходить только, если мы уже на сервере, так?
	if _, err := talk.SendKeepAlive(); err != nil {
		log.Errorf("Try to send initial KeepAlive, got error: %s", err)

		gTomb.Kill(err)
	}

	log.Info("Connected")

	// Джойнимся к чятикам, но делаем это в фоне, чтобы не блочиться на ошибках, например, если бота забанили
	for _, room := range config.Jabber.Channels {
		go joinMuc(room.Name)
	}

	go RotateStatus("")

	lastActivity = time.Now().Unix()
	connecting = false
	isConnected = true

	log.Debugf("Sending disco#info to %s", config.Jabber.Server)

	_, err = talk.DiscoverInfo(talk.JID(), config.Jabber.Server)

	if err != nil {
		log.Infof("Unable to send disco#info to jabber server: %s", err)
		gTomb.Kill(err)

		return
	}
}

// joinMu джойнится к конференциям/каналам/комнатам в джаббере.
func joinMuc(room string) {
	log.Debugf("Sending disco#info from %s to %s", talk.JID(), room)

	if _, err := talk.DiscoverInfo(talk.JID(), room); err != nil {
		log.Infof("Unable to send disco#info to MUC %s: %s", room, err)

		gTomb.Kill(err)
	}

	// Ждём, пока muc нам вернёт список фичей.
	for i := 0; i < (20 * int(config.Jabber.ConnectionTimeout)); i++ {
		var (
			myRoom    interface{}
			supported bool
			exist     bool
		)

		time.Sleep(50 * time.Millisecond)

		if myRoom, exist = mucCapsList.Get(room); !exist {
			// Пока не задискаверилась
			continue
		}

		if supported, exist = myRoom.(map[string]bool)["muc_unsecured"]; exist {
			if supported {
				break
			}

			log.Infof("Unable to join to password-protected room. Don't know how to enter passwords :)")

			return
		}
	}

	// Пытаемся зайти в комнату
	if _, err := talk.JoinMUCNoHistory(room, getBotNickFromRoomConfig(room)); err != nil {
		log.Errorf("Unable to join to MUC: %s", room)

		gTomb.Kill(err)
	}

	log.Infof("Joining to MUC: %s", room)

	// Ждём, когда прилетит presence из комнаты, тогда мы точно знаем, что мы вошли.
	entered := false

	for i := 0; i < (20 * int(config.Jabber.ConnectionTimeout)); i++ {
		time.Sleep(50 * time.Millisecond)

		if slices.Contains(roomsConnected, room) {
			entered = true

			break
		}
	}

	if !entered {
		log.Errorf(
			"Unable to enter to MUC %s, join timeout after %d seconds (server does not return my presence for this room)",
			room,
			20*int(config.Jabber.ConnectionTimeout)+1,
		)

		return
	}

	// Вот теперь точно можно слать статус.
	log.Infof("Joined to MUC: %s", room)

	go RotateStatus(room)
}

// probeServerLiveness проверяет живость соединения с сервером. Для многих серверов обязательная штука, без которой
// они выкидывают клиента через некоторое время неактивности.
func probeServerLiveness() { //nolint:gocognit
	defer gTomb.Done()

	for {
		select {
		case <-gTomb.Dying():
			return

		default:
			for {
				if shutdown {
					return
				}

				sleepTime := time.Duration(config.Jabber.ServerPingDelay) * 1000 * time.Millisecond
				sleepTime += time.Duration(rand.Int63n(1000*config.Jabber.PingSplayDelay)) * time.Millisecond
				time.Sleep(sleepTime)

				if !isConnected {
					continue
				}

				// Пингуем, только если не было никакой активности в течение > config.Jabber.ServerPingDelay,
				// в худшем случе это будет ~ (config.Jabber.PingSplayDelay * 2) + config.Jabber.PingSplayDelay
				// if (time.Now().Unix() - lastServerActivity) < (config.Jabber.ServerPingDelay + config.Jabber.PingSplayDelay) {
				//	continue
				// }

				if serverCapsQueried { // Сервер ответил на disco#info
					var (
						value interface{}
						exist bool
					)

					value, exist = serverCapsList.Get("urn:xmpp:ping")

					switch {
					// Сервер анонсировал, что умеет в c2s пинги
					case exist && value.(bool):
						// Таймаут c2s пинга. Возьмём сумму задержки между пингами, добавим таймаут коннекта и добавим
						// максимальную корректировку разброса.
						txTimeout := config.Jabber.ServerPingDelay + config.Jabber.ConnectionTimeout
						txTimeout += config.Jabber.PingSplayDelay
						rxTimeout := txTimeout

						rxTimeAgo := time.Now().Unix() - serverPingTimestampRx

						if serverPingTimestampTx > 0 { // Первая пуля от нас ушла...
							switch {
							// Давненько мы не получали понгов от сервера, вероятно, соединение с сервером утеряно?
							case rxTimeAgo > (rxTimeout * 2):
								err := fmt.Errorf( //nolint:goerr113
									"stall connection detected. No c2s pong for %d seconds",
									rxTimeAgo,
								)

								gTomb.Kill(err)

								continue

							// По-умолчанию, мы отправляем c2s пинг
							default:
								log.Debugf("Sending c2s ping from %s to %s", talk.JID(), config.Jabber.Server)

								if err := talk.PingC2S(talk.JID(), config.Jabber.Server); err != nil {
									gTomb.Kill(err)

									continue
								}

								serverPingTimestampTx = time.Now().Unix()
							}
						} else { // Первая пуля пока не вылетела, отправляем
							log.Debugf("Sending first c2s ping from %s to %s", talk.JID(), config.Jabber.Server)

							if err := talk.PingC2S(talk.JID(), config.Jabber.Server); err != nil {
								gTomb.Kill(err)

								continue
							}

							serverPingTimestampTx = time.Now().Unix()
						}

					// Сервер не анонсировал, что умеет в c2s пинги
					default:
						log.Debug("Sending keepalive whitespace ping")

						if _, err := talk.SendKeepAlive(); err != nil {
							gTomb.Kill(err)

							continue
						}
					}
				} else { // Сервер не ответил на disco#info
					log.Debug("Sending keepalive whitespace ping")

					if _, err := talk.SendKeepAlive(); err != nil {
						gTomb.Kill(err)

						continue
					}
				}
			}
		}
	}
}

// probeMUCLiveness Пингует MUC-и, нужно для проверки, что клиент ещё находится в MUC-е.
func probeMUCLiveness() { //nolint:gocognit
	for {
		select {
		case <-gTomb.Dying():
			return

		default:
			for {
				for _, room := range roomsConnected {
					var (
						exist          bool
						lastActivityTS interface{}
					)

					// Если записи про комнату нету, то пинговать её бессмысленно.
					if lastActivityTS, exist = lastMucActivity.Get(room); !exist {
						continue
					}

					// Если время последней активности в чятике не превысило
					// config.Jabber.ServerPingDelay + config.Jabber.PingSplayDelay, ничего не пингуем.
					if (time.Now().Unix() - lastActivityTS.(int64)) < (config.Jabber.ServerPingDelay + config.Jabber.PingSplayDelay) {
						continue
					}

					/* Пинг MUC-а по сценарию без серверной оптимизации мы реализовывать не будем. Это как-то не надёжно.
					go func(room string) {
						// Небольшая рандомная задержка перед пингом комнаты
						sleepTime := time.Duration(rand.Int63n(1000*config.Jabber.PingSplayDelay)) * time.Millisecond //nolint:gosec
						time.Sleep(sleepTime)

						if err := talk.PingS2S(talk.JID(), room+"/"+getBotNickFromRoomConfig(room)); err != nil {
							gTomb.Kill(err)
							continue
						}
					}(room)
					*/

					var roomMap interface{}

					roomMap, exist = mucCapsList.Get(room)

					// Пинги комнаты проводим, только если она записана, как прошедшая disco#info и поддерживающая
					// Server Optimization.
					if exist && roomMap.(map[string]bool)["http://jabber.org/protocol/muc#self-ping-optimization"] {
						go func(room string) {
							// Небольшая рандомная задержка перед пингом комнаты.
							sleepTime := time.Duration(rand.Int63n(1000*config.Jabber.PingSplayDelay)) * time.Millisecond
							time.Sleep(sleepTime)

							log.Debugf("Sending MUC ping from %s to %s", talk.JID(), room)

							if err := talk.PingS2S(talk.JID(), room); err != nil {
								err := fmt.Errorf("unable to ping MUC %s: %w", room, err)
								gTomb.Kill(err)
							}
						}(room)
					}
				}

				time.Sleep(time.Duration(config.Jabber.MucPingDelay) * time.Second)
			}
		}
	}
}

// RotateStatus периодически изменяет статус бота в MUC-е согласно настройкам из кофига.
func RotateStatus(room string) {
	defer gTomb.Done()

	for {
		select {
		case <-gTomb.Dying():
			return

		default:
			// TODO: Переделать на ticker-ы
			totalSleepTime := time.Duration(config.Jabber.RuntimeStatus.RotationTime) * time.Second
			totalSleepTime += time.Duration(config.Jabber.RuntimeStatus.RotationSplayTime) * time.Second

			for {
				status := randomPhrase(config.Jabber.RuntimeStatus.Text)
				log.Debugf("Set status for MUC: %s to: %s", room, status)

				var p xmpp.Presence

				if room != "" {
					p = xmpp.Presence{ //nolint:exhaustruct
						To:     room,
						Status: status,
					}
				} else {
					p = xmpp.Presence{ //nolint:exhaustruct
						Status: status,
					}
				}

				if _, err := talk.SendPresence(p); err != nil {
					log.Infof("Unable to send presence to MUC %s: %s", room, err)
					gTomb.Kill(err)

					return
				}

				// Если мы не хотим ротировать, то цикл нам тут не нужен, просто выходим.
				if config.Jabber.RuntimeStatus.RotationTime == 0 {
					gTomb.Done()

					return
				}

				time.Sleep(totalSleepTime)
			}
		}
	}
}

// randomPhrase Выдаёт одну рандомную фразу из даденного списка фраз.
func randomPhrase(list []string) string {
	phrase := ""

	if listLen := len(list); listLen > 0 {
		phrase = list[rand.Intn(listLen)]
	}

	return phrase
}

// interfaceToStringSlice превращает данный интерфейс в слайс строк.
// Если может, конечно :) .
func interfaceToStringSlice(iface interface{}) []string {
	var mySlice []string

	// А теперь мы начинаем дурдом, нам надо превратить ёбанный interface{} в []string
	// Поскольку interface{} может быть чем угодно, перестрахуемся
	if reflect.TypeOf(iface).Kind() == reflect.Slice {
		shit := reflect.ValueOf(iface)

		for i := 0; i < shit.Len(); i++ {
			mySlice = append(mySlice, fmt.Sprint(shit.Index(i)))
		}
	}

	return mySlice
}

// getRealJIDfromNick достаёт из запомненных presence-ов по даденному nick-у real jid с resource-ом. Nick должен
// содержать имя конфы, откуда участник.
func getRealJIDfromNick(fullNick string) string {
	var p xmpp.Presence

	room := (strings.SplitN(fullNick, "/", 2))[0]

	// Достанем presence участника
	presenceJSONInterface, present := roomPresences.Get(room)

	// Никого нет дома
	if !present {
		return ""
	}

	presenceJSONStrings := interfaceToStringSlice(presenceJSONInterface)

	for _, presepresenceJSONString := range presenceJSONStrings {
		_ = json.Unmarshal([]byte(presepresenceJSONString), &p)

		if p.From == fullNick {
			return p.JID
		}
	}

	return ""
}

// isMucAdmin возращает true если указанный nick является овнером или админом в чяти.
func isMucAdmin(fullNick string) bool {
	var p xmpp.Presence

	room := (strings.SplitN(fullNick, "/", 2))[0]

	// Достанем presence участника
	presenceJSONInterface, present := roomPresences.Get(room)

	// Никого нет дома
	if !present {
		return false
	}

	presenceJSONStrings := interfaceToStringSlice(presenceJSONInterface)

	for _, presepresenceJSONString := range presenceJSONStrings {
		_ = json.Unmarshal([]byte(presepresenceJSONString), &p)

		if p.From == fullNick {
			if p.Affiliation == "owner" || p.Affiliation == "admin" {
				return true
			}
		}
	}

	return false
}

// getMucNames возвращает список участников конференции.
func getMucNames(room string) []string {
	var (
		names []string
		p     xmpp.Presence
	)

	// Достанем presence участников
	presenceJSONInterface, present := roomPresences.Get(room)

	if !present {
		return names
	}

	// Достанем presence участника
	presenceJSONStrings := interfaceToStringSlice(presenceJSONInterface)

	for _, presepresenceJSONString := range presenceJSONStrings {
		_ = json.Unmarshal([]byte(presepresenceJSONString), &p)

		names = append(names, p.From)
	}

	return names
}

// getBotNickFromRoomConfig достаёт (короткий) ник бота из настроек чата
func getBotNickFromRoomConfig(room string) string {
	for _, roomStruct := range config.Jabber.Channels {
		if roomStruct.Name == room {
			return roomStruct.Nick
		}
	}

	return config.Jabber.Nick
}

/* vim: set ft=go noet ai ts=4 sw=4 sts=4: */
