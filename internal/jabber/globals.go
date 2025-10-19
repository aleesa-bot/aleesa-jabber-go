package jabber

import (
	"aleesa-jabber-go/internal/anycollection"
	"context"
	"os"

	"github.com/cockroachdb/pebble"
	"github.com/eleksir/go-xmpp"
	"github.com/go-redis/redis/v8"
	"gopkg.in/tomb.v2"
)

// Config - это у нас глобальная штука :) .
var Config myConfig

// Статичные пути, по которым у нас лежат конфиги. При запуске сюда дописываются и динамические пути тоже.
var ConfigLocations = []string{
	"~/.aleesa-jabber-go.json",
	"~/aleesa-jabber-go.json",
	"/etc/aleesa-jabber-go.json",
}

// To break circular message forwarding we must set some sane default, it can be overridden via Config.
var forwardMax int64 = 5

// Ставится в true, если мы получили сигнал на выключение.
var Shutdown = false

// Чтобы не организовывать драку за установку коннекта.
var connecting = false

// Глобальное состояние соединения.
var IsConnected = false

// Канал, в который приходят уведомления для хэндлера сигналов от траппера сигналов.
var SigChan chan os.Signal

// Основной инстанс xmpp-клиента.
var Talk *xmpp.Client

// Опции подключения к xmpp-серверу.
var Options *xmpp.Options

// Список комнат, в которых находится бот.
var RoomsConnected []string

// Время последней активности, нужно для jabber:iq:last.
var LastActivity int64

// Время последней активности, нужно для c2s пингов - посылаем пинги, только если давненько ничего не приходило с
// сервера.
var LastServerActivity int64

// Время последней активности MUC-ов, нужно для пингов - посылаем пинги, только если давненько ничего не приходило из
// muc-ов.
var LastMucActivity *anycollection.Collection

// Получен ли ответ на запрос disco#info к серверу.
var ServerCapsQueried bool

// sync.Map-ка с капабилити сервера.
var ServerCapsList *anycollection.Collection

// sync.Map-ка с комнатами и их capability.
var MucCapsList *anycollection.Collection

// Время, когда был отправлен c2s ping.
var ServerPingTimestampTx int64

// Время, когда был принят s2c pong.
var ServerPingTimestampRx int64

// Объектик для хранения стейта утилизатора горутинок.
var GTomb tomb.Tomb

// sync.Map-ка со списком участников конференций (в json-формате, согласно структуре xmpp.Presence, "room".[]json).
var RoomPresences *anycollection.Collection

// Объектики клиента-редиски.
var RedisClient *redis.Client
var Subscriber *redis.PubSub

// Мапка с открытыми дескрипторами баз с настройками.
var settingsDB = make(map[string]*pebble.DB)

// Main context.
var Ctx = context.Background()

/* vim: set ft=go noet ai ts=4 sw=4 sts=4: */
