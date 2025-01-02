package main

import (
	"context"
	"os"

	"github.com/cockroachdb/pebble"
	"github.com/eleksir/go-xmpp"
	"github.com/go-redis/redis/v8"
	"gopkg.in/tomb.v2"
)

// Config - это у нас глобальная штука :) .
var config myConfig

// Статичные пути, по которым у нас лежат конфиги. При запуске сюда дописываются и динамические пути тоже.
var configLocations = []string{
	"~/.aleesa-jabber-go.json",
	"~/aleesa-jabber-go.json",
	"/etc/aleesa-jabber-go.json",
}

// To break circular message forwarding we must set some sane default, it can be overridden via config.
var forwardMax int64 = 5

// Ставится в true, если мы получили сигнал на выключение.
var shutdown = false

// Чтобы не организовывать драку за установку коннекта.
var connecting = false

// Глобальное состояние соединения.
var isConnected = false

// Канал, в который приходят уведомления для хэндлера сигналов от траппера сигналов.
var sigChan chan os.Signal

// Основной инстанс xmpp-клиента.
var talk *xmpp.Client

// Опции подключения к xmpp-серверу.
var options *xmpp.Options

// Список комнат, в которых находится бот.
var roomsConnected []string

// Время последней активности, нужно для jabber:iq:last.
var lastActivity int64

// Время последней активности, нужно для c2s пингов - посылаем пинги, только если давненько ничего не приходило с
// сервера.
var lastServerActivity int64

// Время последней активности MUC-ов, нужно для пингов - посылаем пинги, только если давненько ничего не приходило из
// muc-ов.
var lastMucActivity *Collection

// Получен ли ответ на запрос disco#info к серверу.
var serverCapsQueried bool

// sync.Map-ка с капабилити сервера.
var serverCapsList *Collection

// sync.Map-ка с комнатами и их capability.
var mucCapsList *Collection

// Время, когда был отправлен c2s ping.
var serverPingTimestampTx int64

// Время, когда был принят s2c pong.
var serverPingTimestampRx int64

// Объектик для хранения стейта утилизатора горутинок.
var gTomb tomb.Tomb

// sync.Map-ка со списком участников конференций (в json-формате, согласно структуре xmpp.Presence, "room".[]json).
var roomPresences *Collection

// Объектики клиента-редиски.
var redisClient *redis.Client
var subscriber *redis.PubSub

// Мапка с открытыми дескрипторами баз с настройками.
var settingsDB = make(map[string]*pebble.DB)

// Main context.
var ctx = context.Background()

/* vim: set ft=go noet ai ts=4 sw=4 sts=4: */
