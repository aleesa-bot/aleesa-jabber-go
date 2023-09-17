package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"

	"github.com/cockroachdb/errors/oserror"
	"github.com/cockroachdb/pebble"
	log "github.com/sirupsen/logrus"
)

// StoreKV сохраняет в указанной бд ключ и значение.
func StoreKV(db *pebble.DB, key string, value string) error {
	var (
		kArray = []byte(key) //nolint:revive
		vArray = []byte(value)
	)

	err := db.Set(kArray, vArray, pebble.Sync)

	return err
}

// FetchV достаёт значение по ключу.
func FetchV(db *pebble.DB, key string) (string, error) {
	var (
		kArray      = []byte(key) //nolint:revive
		vArray      []byte
		valueString string
	)

	vArray, closer, err := db.Get(kArray)

	if err != nil {
		return valueString, err
	}

	valueString = string(vArray)
	err = closer.Close()

	return valueString, err
}

// Достанем настройку из БД с настройками.
func getSetting(chatID string, setting string) string {
	var (
		err      error
		chatHash = sha256.Sum256([]byte(chatID))
		database = fmt.Sprintf("settings_db/%x", chatHash)
	)

	// Если БД не открыта, откроем её
	if _, ok := settingsDB[database]; !ok {
		var options pebble.Options
		// По дефолту ограничение ставится на мегабайты данных, а не на количество файлов, поэтому с дефолтными
		// настройками порождается огромное количество файлов. Умолчальное ограничение на количество файлов - 500 штук,
		// что нас не устраивает, поэтому немного снизим эту цифру до более приемлемых значений
		options.L0CompactionFileThreshold = 8
		settingsDB[database], err = pebble.Open(config.DataDir+"/"+database, &options)

		if err != nil {
			log.Errorf("Unable to open settings db, %s: %s\n", database, err)

			return ""
		}
	}

	value, err := FetchV(settingsDB[database], setting)

	// Если из базы ничего не вынулось, по каким-то причинам, то просто вернём пустую строку
	if err != nil {
		switch {
		case errors.Is(err, pebble.ErrNotFound):
			log.Debugf("Unable to get value for %s: no record found in db %s", setting, database)

		case errors.Is(err, fs.ErrNotExist):
			log.Debugf("Unable to get value for %s: db dir %s does not exist", setting, database)

		case errors.Is(err, oserror.ErrNotExist):
			log.Debugf("Unable to get value for %s: db dir %s does not exist", setting, database)

		default:
			log.Errorf("Unable to get value for %s in db dir %s: %s", setting, database, err)
		}

		return ""
	}

	return value
}

// Сохраним настройку в БД с настройками.
func saveSetting(chatID string, setting string, value string) error {
	var (
		chatHash = sha256.Sum256([]byte(chatID))
		database = fmt.Sprintf("settings_db/%x", chatHash)
		err      error
	)

	// Если БД не открыта, откроем её
	if _, ok := settingsDB[database]; !ok {
		var options pebble.Options
		// По дефолту ограничение ставится на мегабайты данных, а не на количество файлов, поэтому с дефолтными
		// настройками порождается огромное количество файлов. Умолчальное ограничение на количество файлов - 500 штук,
		// что нас не устраивает, поэтому немного снизим эту цифру до более приемлемых значений
		options.L0CompactionFileThreshold = 8

		settingsDB[database], err = pebble.Open(config.DataDir+"/"+database, &options)

		if err != nil {
			log.Errorf("Unable to open settings db, %s: %s\n", database, err)

			return err
		}
	}

	if err := StoreKV(settingsDB[database], setting, value); err != nil {
		log.Errorf("Unable to save setting %s in database %s: %s", setting, database, err)
	}

	return err
}

/* vim: set ft=go noet ai ts=4 sw=4 sts=4: */
