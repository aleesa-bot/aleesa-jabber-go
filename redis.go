package main

import (
	"encoding/json"
	"fmt"

	"github.com/eleksir/go-xmpp"
	"github.com/go-redis/redis/v8"
	log "github.com/sirupsen/logrus"
)

func redisLoop(redisMsgChan <-chan *redis.Message) error {
	// Обработчик событий от редиски
	for msg := range redisMsgChan {
		if shutdown {
			return nil
		}

		if err := redisMsgParser(msg.Payload); err != nil {
			log.Warn(err)
		}
	}

	return nil
}

// redisMsgParser парсит json-чики прилетевшие из REDIS-ки.
func redisMsgParser(msg string) error {
	var err error

	if shutdown {
		// Если мы завершаем работу программы, то нам ничего обрабатывать не надо.
		return err
	}

	var j rMsg

	log.Debugf("Incoming raw json: %s", msg)

	if err := json.Unmarshal([]byte(msg), &j); err != nil {
		log.Warnf("Unable to to parse message from redis channel: %s", err)

		return nil
	}

	// Validate our j
	if exist := j.From; exist == "" {
		log.Warnf("Incorrect msg from redis, no from field: %s", msg)

		return nil
	}

	if exist := j.Chatid; exist == "" {
		log.Warnf("Incorrect msg from redis, no chatid field: %s", msg)

		return nil
	}

	if exist := j.Userid; exist == "" {
		log.Warnf("Incorrect msg from redis, no userid field: %s", msg)

		return nil
	}

	if exist := j.Message; exist == "" {
		log.Warnf("Incorrect msg from redis, no message field: %s", msg)

		return nil
	}

	if exist := j.Plugin; exist == "" {
		log.Warnf("Incorrect msg from redis, no plugin field: %s", msg)

		return nil
	}

	if exist := j.Mode; exist == "" {
		log.Warnf("Incorrect msg from redis, no mode field: %s", msg)

		return nil
	}

	// j.Misc.Answer может и не быть, тогда ответа на такое сообщение не будет
	if j.Misc.Answer == 0 {
		log.Debug("Field Misc->Answer = 0, skipping message")

		return nil
	}

	// j.Misc.BotNick тоже можно не передавать, тогда будет записана пустая строка
	// j.Misc.CSign если нам его не передали, возьмём значение из конфига
	if exist := j.Misc.Csign; exist == "" {
		j.Misc.Csign = config.CSign
	}

	// j.Misc.FwdCnt если нам его не передали, то будет 0
	if exist := j.Misc.Fwdcnt; exist == 0 {
		j.Misc.Fwdcnt = 1
	}

	// j.Misc.GoodMorning может быть быть 1 или 0, по-умолчанию 0
	// j.Misc.MsgFormat может быть быть 1 или 0, по-умолчанию 0
	// j.Misc.Username можно не передавать, тогда будет пустая строка

	// Отвалидировались, теперь вернёмся к нашим баранам.
	if j.Misc.Answer == 1 {
		switch j.Mode {
		case "public":
			// Отправляем сообщение в чятик, а не тому, кто прислал нам исходное сообщение
			if _, err := talk.Send(
				xmpp.Chat{ //nolint:exhaustruct
					Remote: j.Chatid,
					Type:   "groupchat",
					Text:   j.Message,
				},
			); err != nil {
				err = fmt.Errorf("unable to send phrase to room %s: %w", j.Chatid, err)

				return err
			}

		case "private":
			// Отправляем сообщение тому, кто прислал сообщение
			if _, err := talk.Send(
				xmpp.Chat{ //nolint:exhaustruct
					Remote: j.From,
					Type:   "chat",
					Text:   j.Message,
				},
			); err != nil {
				err = fmt.Errorf("unable to send phrase to jid %s: %w", j.From, err)

				return err
			}
		default:
			log.Infof("Got message from redis that neither public or private: %s", msg)
		}
	}

	return err
}

/* vim: set ft=go noet ai ts=4 sw=4 sts=4: */
