package jabber

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"time"

	"aleesa-jabber-go/internal/log"

	"github.com/eleksir/go-xmpp"

	"golang.org/x/exp/slices"
)

// EstablishConnection устанавливает соединение с jabber-сервером.
func EstablishConnection() error {
	var err error

	if connecting && !IsConnected {
		return nil
	}

	// Проставляем глобальные переменные.
	connecting = true
	IsConnected = false
	RoomsConnected = make([]string, 0)

	Talk, err = Options.NewClient()

	if err != nil {
		return fmt.Errorf("unable to connect to %s: %w", Options.Host, err)
	}

	// По идее keepalive должен же проходить только, если мы уже на сервере, так?
	if _, err := Talk.SendKeepAlive(); err != nil {
		return fmt.Errorf("try to send initial KeepAlive, got error: %w", err)
	}

	log.Info("Connected")

	// Джойнимся к чятикам, но делаем это в фоне, чтобы не блочиться на ошибках, например, если бота забанили
	for _, room := range Config.Jabber.Channels {
		GTomb.Go(func() error { return joinMuc(room.Name) })
	}

	GTomb.Go(func() error { return RotateStatus("") })

	LastActivity = time.Now().Unix()
	connecting = false
	IsConnected = true

	log.Debugf("Sending disco#info to %s", Config.Jabber.Server)

	_, err = Talk.DiscoverInfo(Talk.JID(), Config.Jabber.Server)

	if err != nil {
		return fmt.Errorf("unable to send disco#info to jabber server: %w", err)
	}

	return nil
}

// joinMu джойнится к конференциям/каналам/комнатам в джаббере.
func joinMuc(room string) error {
	log.Debugf("Sending disco#info from %s to %s", Talk.JID(), room)

	if _, err := Talk.DiscoverInfo(Talk.JID(), room); err != nil {
		return fmt.Errorf("unable to send disco#info to MUC %s: %w", room, err)
	}

	// Ждём, пока muc нам вернёт список фичей.
	for i := 0; i < (20 * int(Config.Jabber.ConnectionTimeout)); i++ {
		var (
			myRoom    interface{}
			supported bool
			exist     bool
		)

		time.Sleep(50 * time.Millisecond)

		if myRoom, exist = MucCapsList.Get(room); !exist {
			// Пока не задискаверилась
			continue
		}

		if supported, exist = myRoom.(map[string]bool)["muc_unsecured"]; exist {
			if supported {
				break
			}

			log.Infof("Unable to join to password-protected room. Don't know how to enter passwords :)")

			return nil
		}
	}

	// Пытаемся зайти в комнату
	if _, err := Talk.JoinMUCNoHistory(room, getBotNickFromRoomConfig(room)); err != nil {
		return fmt.Errorf("unable to join to MUC: %s, %w", room, err)
	}

	log.Infof("Joining to MUC: %s", room)

	// Ждём, когда прилетит presence из комнаты, тогда мы точно знаем, что мы вошли.
	entered := false

	for i := 0; i < (20 * int(Config.Jabber.ConnectionTimeout)); i++ {
		time.Sleep(50 * time.Millisecond)

		if slices.Contains(RoomsConnected, room) {
			entered = true

			break
		}
	}

	if !entered {
		log.Errorf(
			"Unable to enter to MUC %s, join timeout after %d seconds (server does not return my presence for this room)",
			room,
			20*int(Config.Jabber.ConnectionTimeout)+1,
		)

		return nil
	}

	// Вот теперь точно можно слать статус.
	log.Infof("Joined to MUC: %s", room)

	GTomb.Go(func() error { return RotateStatus(room) })

	return nil
}

// ProbeServerLiveness проверяет живость соединения с сервером. Для многих серверов обязательная штука, без которой
// они выкидывают клиента через некоторое время неактивности.
func ProbeServerLiveness() error { //nolint:gocognit
	for {
		for {
			if Shutdown {
				return nil
			}

			sleepTime := time.Duration(Config.Jabber.ServerPingDelay) * 1000 * time.Millisecond
			sleepTime += time.Duration(rand.Int63n(1000*Config.Jabber.PingSplayDelay)) * time.Millisecond
			time.Sleep(sleepTime)

			if !IsConnected {
				continue
			}

			// Пингуем, только если не было никакой активности в течение > Config.Jabber.ServerPingDelay,
			// в худшем случе это будет ~ (Config.Jabber.PingSplayDelay * 2) + Config.Jabber.PingSplayDelay
			// if (time.Now().Unix() - lastServerActivity) < (Config.Jabber.ServerPingDelay + Config.Jabber.PingSplayDelay) {
			//	continue
			// }

			if ServerCapsQueried { // Сервер ответил на disco#info
				var (
					value interface{}
					exist bool
				)

				value, exist = ServerCapsList.Get("urn:xmpp:ping")

				switch {
				// Сервер анонсировал, что умеет в c2s пинги
				case exist && value.(bool):
					// Таймаут c2s пинга. Возьмём сумму задержки между пингами, добавим таймаут коннекта и добавим
					// максимальную корректировку разброса.
					txTimeout := Config.Jabber.ServerPingDelay + Config.Jabber.ConnectionTimeout
					txTimeout += Config.Jabber.PingSplayDelay
					rxTimeout := txTimeout

					rxTimeAgo := time.Now().Unix() - ServerPingTimestampRx

					if ServerPingTimestampTx > 0 { // Первая пуля от нас ушла...
						switch {
						// Давненько мы не получали понгов от сервера, вероятно, соединение с сервером утеряно?
						case rxTimeAgo > (rxTimeout * 2):
							err := fmt.Errorf( //nolint:goerr113
								"stall connection detected. No c2s pong for %d seconds",
								rxTimeAgo,
							)

							return err

						// По-умолчанию, мы отправляем c2s пинг
						default:
							log.Debugf("Sending c2s ping from %s to %s", Talk.JID(), Config.Jabber.Server)

							if err := Talk.PingC2S(Talk.JID(), Config.Jabber.Server); err != nil {
								return err
							}

							ServerPingTimestampTx = time.Now().Unix()
						}
					} else { // Первая пуля пока не вылетела, отправляем
						log.Debugf("Sending first c2s ping from %s to %s", Talk.JID(), Config.Jabber.Server)

						if err := Talk.PingC2S(Talk.JID(), Config.Jabber.Server); err != nil {
							return err
						}

						ServerPingTimestampTx = time.Now().Unix()
					}

				// Сервер не анонсировал, что умеет в c2s пинги
				default:
					log.Debug("Sending keepalive whitespace ping")

					if _, err := Talk.SendKeepAlive(); err != nil {
						return err
					}
				}
			} else { // Сервер не ответил на disco#info
				log.Debug("Sending keepalive whitespace ping")

				if _, err := Talk.SendKeepAlive(); err != nil {
					return err
				}
			}
		}
	}
}

// ProbeMUCLiveness Пингует MUC-и, нужно для проверки, что клиент ещё находится в MUC-е.
func ProbeMUCLiveness() { //nolint:gocognit
	for {
		for {
			for _, room := range RoomsConnected {
				var (
					exist          bool
					lastActivityTS interface{}
				)

				// Если записи про комнату нету, то пинговать её бессмысленно.
				if lastActivityTS, exist = LastMucActivity.Get(room); !exist {
					continue
				}

				// Если время последней активности в чятике не превысило
				// Config.Jabber.ServerPingDelay + Config.Jabber.PingSplayDelay, ничего не пингуем.
				if (time.Now().Unix() - lastActivityTS.(int64)) < (Config.Jabber.ServerPingDelay + Config.Jabber.PingSplayDelay) {
					continue
				}

				/* Пинг MUC-а по сценарию без серверной оптимизации мы реализовывать не будем. Это как-то не надёжно.
				go func(room string) {
					// Небольшая рандомная задержка перед пингом комнаты
					sleepTime := time.Duration(rand.Int63n(1000*Config.Jabber.PingSplayDelay)) * time.Millisecond //nolint:gosec
					time.Sleep(sleepTime)

					if err := talk.PingS2S(talk.JID(), room+"/"+getBotNickFromRoomConfig(room)); err != nil {
						gTomb.Kill(err)
						continue
					}
				}(room)
				*/

				var roomMap interface{}

				roomMap, exist = MucCapsList.Get(room)

				// Пинги комнаты проводим, только если она записана, как прошедшая disco#info и поддерживающая
				// Server Optimization.
				if exist && roomMap.(map[string]bool)["http://jabber.org/protocol/muc#self-ping-optimization"] {
					GTomb.Go(
						func() error {
							// Небольшая рандомная задержка перед пингом комнаты.
							sleepTime := time.Duration(rand.Int63n(1000*Config.Jabber.PingSplayDelay)) * time.Millisecond
							time.Sleep(sleepTime)

							log.Debugf("Sending MUC ping from %s to %s", Talk.JID(), room)

							if err := Talk.PingS2S(Talk.JID(), room); err != nil {
								log.Errorf("unable to ping MUC %s: %s", room, err)
							}

							return nil
						},
					)
				}
			}

			time.Sleep(time.Duration(Config.Jabber.MucPingDelay) * time.Second)
		}
	}
}

// RotateStatus периодически изменяет статус бота в MUC-е согласно настройкам из кофига.
func RotateStatus(room string) error {
	for {
		// TODO: Переделать на ticker-ы
		totalSleepTime := time.Duration(Config.Jabber.RuntimeStatus.RotationTime) * time.Second
		totalSleepTime += time.Duration(Config.Jabber.RuntimeStatus.RotationSplayTime) * time.Second

		for {
			status := RandomPhrase(Config.Jabber.RuntimeStatus.Text)
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

			if _, err := Talk.SendPresence(p); err != nil {
				return fmt.Errorf("unable to send presence to MUC %s: %w", room, err)
			}

			// Если мы не хотим ротировать, то цикл нам тут не нужен, просто выходим.
			if Config.Jabber.RuntimeStatus.RotationTime == 0 {
				return nil
			}

			time.Sleep(totalSleepTime)
		}
	}
}

// RandomPhrase Выдаёт одну рандомную фразу из даденного списка фраз.
func RandomPhrase(list []string) string {
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
	presenceJSONInterface, present := RoomPresences.Get(room)

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

	// Достанем presence участника.
	presenceJSONInterface, present := RoomPresences.Get(room)

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
	presenceJSONInterface, present := RoomPresences.Get(room)

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

// getBotNickFromRoomConfig достаёт (короткий) ник бота из настроек чата.
func getBotNickFromRoomConfig(room string) string {
	for _, roomStruct := range Config.Jabber.Channels {
		if roomStruct.Name == room {
			return roomStruct.Nick
		}
	}

	return Config.Jabber.Nick
}

/* vim: set ft=go noet ai ts=4 sw=4 sts=4: */
