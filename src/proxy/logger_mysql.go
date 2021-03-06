package proxy

import (
	_ "github.com/go-sql-driver/mysql"
	"sync"
)

type messageDao struct {
	mysqlDao *mysqlDao
}

var messageMysqlDao *messageDao
var messageOnce sync.Once

func LoggerMessageDao() *messageDao {
	messageOnce.Do(func() {
		var mysqlDaoInstance *mysqlDao = nil

		if len(GConfig.LoggerMysqlConfig.Ip) > 0 && len(GConfig.LoggerMysqlConfig.Username) > 0 {
			mysqlDaoInstance = NewMysqlDao("logger", GConfig.LoggerMysqlConfig)
		}

		messageMysqlDao = &messageDao{
			mysqlDao: mysqlDaoInstance,
		}
	})

	return messageMysqlDao
}

func (obj *messageDao) RecordMessage(pubSubMessage *PubSubMessage) bool {
	if obj.mysqlDao == nil {
		Logger.Print("mysql logger not open")
		return false
	}

	logMessage, ok := pubSubMessage.Message.(LogMessage)
	if !ok {
		return false
	}

	stmtIns, err := obj.mysqlDao.db.Prepare("INSERT INTO access_logs(user_id,user_ip,user_agent,resource,type,content,time) VALUES (?,?,?,?,?,?,?)")
	if err != nil {
		Logger.Print(err)
		obj.mysqlDao.Reconnect()
		LoggerMessageRecord.RecordMessage(pubSubMessage)
		return false
	}
	defer stmtIns.Close()

	if _, err = stmtIns.Exec(
		logMessage.UserID,
		pubSubMessage.IP,
		pubSubMessage.UserAgent,
		logMessage.Resource,
		logMessage.Type,
		logMessage.Content,
		pubSubMessage.Time/1000); err != nil {
		Logger.Print(err)
		return false
	}

	return true
}

func (obj *messageDao) Close() {
	if obj.mysqlDao == nil {
		return
	}

	Logger.Printf("mysql dao[%s] will close", obj.mysqlDao.name)

	obj.mysqlDao.Close()
}
