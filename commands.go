package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/eleksir/go-xmpp"
	log "github.com/sirupsen/logrus"
)

func cmd(v xmpp.Chat) error {
	var (
		err     error
		message sMsg
		data    []byte
	)

	message.From = config.Redis.MyChannel
	message.Threadid = "" // Тредиков в jabber-е нету, поэтому это поле отправляем пустым
	message.Message = v.Text
	message.Plugin = config.Redis.MyChannel

	// Список команд, публично доступных, без каких-то специальных настроек
	cmds := []string{"ping", "пинг", "пинх", "pong", "понг", "понх", "coin", "монетка", "roll", "dice", "кости",
		"ver", "version", "версия", "хэлп", "halp", "kde", "кде", "lat", "лат", "friday", "пятница", "proverb",
		"пословица", "пословиться", "fortune", "фортунка", "f", "ф", "anek", "анек", "анекдот", "buni", "cat",
		"кис", "drink", "праздник", "fox", "лис", "frog", "лягушка", "horse", "лошадь", "лошадка", "monkeyuser",
		"owl", "сова", "сыч", "rabbit", "bunny", "кролик", "snail", "улитка", "xkcd", "dig", "копать", "fish",
		"fishing", "рыба", "рыбка", "рыбалка", "karma", "карма"}

	// Список отключаемых команд проверяется в case-е про groupchat, чуть ниже

	// Список "сложных" команд, это те, которые имеют ключевые данные на конце команды. Пока что это только погодка.
	cmdsComplex := []string{"w ", "п ", "погода ", "погодка ", "погадка ", "weather "}

	// Список команд бармэна. Основное отличие в том, что после команды ключевые данные могут быть, а могут и не быть.
	// То есть рюмашку можно заказать себе или кому-то в чятике.
	cmdsBartender := []string{"rum", "ром", "vodka", "водка", "beer", "пиво", "tequila", "текила", "whisky", "виски",
		"absinthe", "абсент"}

	// Есть ещё команда на изменение кармы. Это команда-суффикс ++ или --. Она проверяется по-месту, потому что она одна
	// и вполне уникальна.

	switch v.Type {
	case "groupchat":
		message.Mode = "public"
		message.Chatid = strings.SplitN(v.Remote, "/", 2)[0]
		userNick := strings.SplitN(v.Remote, "/", 2)[1]

		if realJid := getRealJIDfromNick(v.Remote); realJid != "" {
			message.Userid = realJid
		} else {
			message.Userid = v.Remote
		}

		message.Misc.Answer = 0
		message.Misc.Fwdcnt = 0
		message.Misc.Csign = config.CSign
		message.Misc.Username = userNick
		message.Misc.Botnick = getBotNickFromRoomConfig(message.Chatid)
		message.Misc.Msgformat = 0

		msgLen := len(v.Text)

		// TODO: а вот тут надо впилить парсер команд
		if (msgLen > len(config.CSign)) && (v.Text[:len(config.CSign)] == config.CSign) {
			var (
				cmd    = v.Text[len(config.CSign):]
				room   = message.Chatid
				answer string
			)

			// И таких кейсов у нас минимум на команды !help и семейство команд !admin, а также на те команды, которые не всегда работают
			switch {
			case cmd == "help" || cmd == "помощь":
				answer += fmt.Sprintf("%shelp | %sпомощь             - это сообщение\n", config.CSign, config.CSign)
				answer += fmt.Sprintf("%sanek | %sанек | %sанекдот    - рандомный анекдот с anekdot.ru\n", config.CSign, config.CSign, config.CSign)
				answer += fmt.Sprintf("%sbuni                       - комикс-стрип hapi buni\n", config.CSign)
				answer += fmt.Sprintf("%sbunny                      - кролик\n", config.CSign)
				answer += fmt.Sprintf("%srabbit | %sкролик           - кролик\n", config.CSign, config.CSign)
				answer += fmt.Sprintf("%scat | %sкис                 - кошечка\n", config.CSign, config.CSign)
				answer += fmt.Sprintf("%sdice | %sroll | %sкости      - бросить кости\n", config.CSign, config.CSign, config.CSign)
				answer += fmt.Sprintf("%sdig | %sкопать              - заняться археологией\n", config.CSign, config.CSign)
				answer += fmt.Sprintf("%sdrink | %sпраздник          - какой сегодня праздник?\n", config.CSign, config.CSign)
				answer += fmt.Sprintf("%sfish | %sfisher             - порыбачить\n", config.CSign, config.CSign)
				answer += fmt.Sprintf("%sрыба | %sрыбка | %sрыбалка   - порыбачить\n", config.CSign, config.CSign, config.CSign)
				answer += fmt.Sprintf("%sf | %sф                     - рандомная фраза из сборника цитат fortune_mod\n", config.CSign, config.CSign)
				answer += fmt.Sprintf("%sfortune | %sфортунка        - рандомная фраза из сборника цитат fortune_mod\n", config.CSign, config.CSign)
				answer += fmt.Sprintf("%sfox | %sлис                 - лисичка\n", config.CSign, config.CSign)
				answer += fmt.Sprintf("%sfriday | %sпятница          - а не пятница ли сегодня?\n", config.CSign, config.CSign)
				answer += fmt.Sprintf("%sfrog | %sлягушка            - лягушка\n", config.CSign, config.CSign)
				answer += fmt.Sprintf("%shorse | %sлошадь | %sлошадка - лошадка\n", config.CSign, config.CSign, config.CSign)
				answer += fmt.Sprintf("%skarma фраза                - посмотреть карму фразы\n", config.CSign)
				answer += fmt.Sprintf("%sкарма фраза                - посмотреть карму фразы\n", config.CSign)
				answer += fmt.Sprintln("фраза++ | фраза--           - повысить или понизить карму фразы")
				answer += fmt.Sprintf("%slat | %sлат                 - сгенерировать фразу из крылатого латинского выражения\n", config.CSign, config.CSign)
				answer += fmt.Sprintf("%smonkeyuser                 - комикс-стрип MonkeyUser\n", config.CSign)
				answer += fmt.Sprintf("%sowl | %sсова                - сова\n", config.CSign, config.CSign)
				answer += fmt.Sprintf("%sping | %sпинг               - попинговать бота\n", config.CSign, config.CSign)
				answer += fmt.Sprintf("%sproverb | %sпословица       - рандомная русская пословица\n", config.CSign, config.CSign)
				answer += fmt.Sprintf("%ssnail | %sулитка            - улитка\n", config.CSign, config.CSign)
				answer += fmt.Sprintf("%ssome_brew                  - выдать соответствующий напиток, бармен может налить rum, ром, vodka, водку, tequila, текила\n", config.CSign)
				answer += fmt.Sprintf("%sver | %sversion             - написать что-то про версию ПО\n", config.CSign, config.CSign)
				answer += fmt.Sprintf("%sверсия                     - написать что-то про версию ПО\n", config.CSign)
				answer += fmt.Sprintf("%sw город | %sп город         - погода в городе\n", config.CSign, config.CSign)
				answer += fmt.Sprintf("%sxkcd                       - комикс-стрип с xkcb.ru\n", config.CSign)

				// Для овнера или админа надо показывать команду admin
				if isMucAdmin(v.Remote) {
					answer += fmt.Sprintf("%sadmin                      - настройки некоторых плагинов бота для комнаты\n", config.CSign)
				}

				_, err = talk.Send(xmpp.Chat{Remote: room, Type: v.Type, Text: answer}) //nolint:exhaustruct

				return err

			case cmd == "admin":
				if isMucAdmin(v.Remote) {
					answer += fmt.Sprintf("%sadmin oboobs #       - где 1 - вкл, 0 - выкл плагина oboobs\n", config.CSign)
					answer += fmt.Sprintf("%sadmin oboobs         - показываем ли сисечки по просьбе участников чата (команды %stits, %stities, %sboobs, %sboobies, %sсиси, %sсисечки)\n", config.CSign, config.CSign, config.CSign, config.CSign, config.CSign, config.CSign, config.CSign)
					answer += fmt.Sprintf("%sadmin obutts #       - где 1 - вкл, 0 - выкл плагина obutts\n", config.CSign)
					answer += fmt.Sprintf("%sadmin obutts         - показываем ли попки по просьбе участников чата (команды %sass, %sbutt, %sbooty, %sпопа, %sпопка)\n", config.CSign, config.CSign, config.CSign, config.CSign, config.CSign, config.CSign)

					_, err = talk.Send(xmpp.Chat{Remote: room, Type: v.Type, Text: answer}) //nolint:exhaustruct

					return err
				}

				return err

			case cmd == "admin oboobs":
				if isMucAdmin(v.Remote) {
					switch getSetting(room, "oboobs") {
					case "0":
						answer = "Плагин oboobs выключен"
					case "1":
						answer = "Плагин oboobs включен"
					case "":
						_ = saveSetting(room, "oboobs", "0")
						answer = "Плагин oboobs выключен"
					default:
						answer = "Плагин oboobs выключен"
					}

					_, err = talk.Send(xmpp.Chat{Remote: room, Type: v.Type, Text: answer}) //nolint:exhaustruct

					return err
				}

			case cmd == "admin oboobs 1":
				if isMucAdmin(v.Remote) {
					err := saveSetting(room, "oboobs", "1")

					if err != nil {
						answer = "Плагин oboobs всё ещё выключен"
					} else {
						answer = "Плагин oboobs включен"
					}

					_, err = talk.Send(xmpp.Chat{Remote: room, Type: v.Type, Text: answer}) //nolint:exhaustruct

					return err
				}

			case cmd == "admin oboobs 0":
				if isMucAdmin(v.Remote) {
					_ = saveSetting(room, "oboobs", "0")
					answer = "Плагин oboobs выключен"

					_, err = talk.Send(xmpp.Chat{Remote: room, Type: v.Type, Text: answer}) //nolint:exhaustruct

					return err
				}

			case cmd == "admin obutts":
				if isMucAdmin(v.Remote) {
					switch getSetting(room, "obutts") {
					case "0":
						answer = "Плагин obutts выключен"
					case "1":
						answer = "Плагин obutts включен"
					case "":
						_ = saveSetting(room, "obutts", "0")
						answer = "Плагин obutts выключен"
					default:
						answer = "Плагин obutts выключен"
					}

					_, err = talk.Send(xmpp.Chat{Remote: room, Type: v.Type, Text: answer}) //nolint:exhaustruct

					return err
				}

			case cmd == "admin obutts 1":
				if isMucAdmin(v.Remote) {
					err := saveSetting(room, "obutts", "1")

					if err != nil {
						answer = "Плагин obutts всё ещё выключен"
					} else {
						answer = "Плагин obutts включен"
					}

					_, err = talk.Send(xmpp.Chat{Remote: room, Type: v.Type, Text: answer}) //nolint:exhaustruct

					return err
				}

			case cmd == "admin obutts 0":
				if isMucAdmin(v.Remote) {
					_ = saveSetting(room, "obutts", "0")
					answer = "Плагин obutts выключен"

					_, err = talk.Send(xmpp.Chat{Remote: room, Type: v.Type, Text: answer}) //nolint:exhaustruct

					return err
				}

			case cmd == "butt" || cmd == "booty" || cmd == "ass" || cmd == "попа" || cmd == "попка":
				if getSetting(room, "obutts") != "1" {
					return err
				}

				message.Misc.Answer = 1

			case cmd == "tits" || cmd == "boobs" || cmd == "tities" || cmd == "boobies" || cmd == "сиси" || cmd == "сисечки":
				if getSetting(room, "oboobs") != "1" {
					return err
				}

				message.Misc.Answer = 1

			// Отлавливаем команды и им проставляем message.Misc.Answer = 1
			default:
				done := false

				// Простые команды
				for _, command := range cmds {
					if cmd == command {
						done = true
						message.Misc.Answer = 1

						break
					}
				}

				// Сложные команды, например, погода
				if !done {
					for _, command := range cmdsComplex {
						cmdLen := len(cmd)

						if cmdLen > len(command) && cmd[0:len(command)] == command {
							done = true
							message.Misc.Answer = 1

							break
						}
					}
				}

				// Сам себе заказал выпивку
				if !done {
					for _, command := range cmdsBartender {
						if cmd == command {
							done = true
							message.Misc.Answer = 1

							break
						}
					}
				}

				// Заказал выпивку кому-то ещё
				if !done {
					re := regexp.MustCompile(" +")
					pile := re.Split(cmd, 2)
					names := getMucNames(room)

					for _, command := range cmdsBartender {
						if pile[0] == command {
							userNick := strings.TrimSpace(pile[1])

							if userNick != "" {
								if slices.Contains(names, userNick) {
									message.Misc.Username = userNick
								} else {
									answer := fmt.Sprintf("Я тут не вижу участника с ником %s", userNick)

									_, err = talk.Send(xmpp.Chat{Remote: room, Type: v.Type, Text: answer}) //nolint:exhaustruct

									return err
								}

								message.Misc.Answer = 1

								break
							}
						}
					}
				}
			}
		}

		// Ну, вот мы и добрались досюда, и... нам осталось понять, не обращаются ли к боту?
		// Дело в том, что ответа требуют только команды и обращения к боту по нику
		// или jid-у, в данном случае, по нику. А всё остальное должно попадать в craniac-а и оставаться там в качестве
		// материала для бредогенератора.

		// Попробуем выискать изменение кармы

		// ++ или -- на конце фразы - это наш кандидат
		if msgLen > len("++") {
			if v.Text[msgLen-len("--"):msgLen] == "--" || v.Text[msgLen-len("++"):msgLen] == "++" {
				// Предполагается, что менять карму мы будем для одной фразы, то есть для 1 строки
				if len(strings.Split(v.Text, "\n")) == 1 {
					// Костыль для кармы
					message.Misc.Answer = 1
				}
			}
		}

		// Предполагается что в канале бот должен отвечать, только если к нему обратились, либо это была команда
		botNick := getBotNickFromRoomConfig(message.Chatid)

		if regexp.MustCompile(regexp.QuoteMeta(botNick)).Match([]byte(message.Message)) {
			message.Misc.Answer = 1
		}

		message.Message = v.Text
		data, err = json.Marshal(message)

		if err != nil {
			return fmt.Errorf("unable to to serialize message for redis: %w", err)
		}

	case "chat":
		message.Mode = "private"
		message.Chatid = v.Remote

		// Jid у нас уникален, а вот nick может, во-первых, разниться от конфы к конфе, а во-вторых, его может взять
		// себе другой пользователь конфы, пока оригинальный владелец ника отсутствует. Но мы предполагаем, что если
		// комната анонимная, то владелец комнаты, приглашая бота, согласен на некоторые компромиссы.
		if realJID := getRealJIDfromNick(v.Remote); realJID != "" {
			message.Userid = realJID
		} else {
			message.Userid = v.Remote
		}

		message.Misc.Answer = 1
		message.Misc.Fwdcnt = 0
		message.Misc.Csign = config.CSign
		message.Misc.Username = message.Userid
		message.Misc.Botnick = config.Jabber.Nick
		message.Misc.Msgformat = 0

		data, err = json.Marshal(message)

		if err != nil {
			return fmt.Errorf("unable to to serialize message for redis: %w", err)
		}
	}

	// Заталкиваем наш json в редиску
	if err := redisClient.Publish(ctx, config.Redis.Channel, data).Err(); err != nil {
		return fmt.Errorf("unable to send data to redis channel %s: %w", config.Redis.Channel, err)
	}

	log.Debugf("Sent msg to redis channel %s: %s", config.Redis.Channel, string(data))

	return err
}

/* vim: set ft=go noet ai ts=4 sw=4 sts=4: */
