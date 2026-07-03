package app

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
)

type MethodHandler func(params json.RawMessage) (interface{}, error)

var (
	registeredMethods map[string]MethodHandler
	registerOnce      sync.Once
)

func RegisterMethods(a *App) {
	registerOnce.Do(func() {
		registeredMethods = make(map[string]MethodHandler)

		register("CheckHasIdentity", a.CheckHasIdentity)
		register("Register", a.Register)
		register("Login", a.Login)

		// Teams
		register("JoinTeam", a.JoinTeam)
		register("JoinTeamWithName", a.JoinTeamWithName)
		register("LeaveTeam", a.LeaveTeam)
		register("SwitchTeam", a.SwitchTeam)
		register("ListTeams", a.ListTeams)
		register("GetActiveTeam", a.GetActiveTeam)
		register("GetPubKey", a.GetPubKey)

		register("SendTeamChat", a.SendTeamChat)
		register("SendPrivateChat", a.SendPrivateChat)
		register("ListConversations", a.ListConversations)
		register("GetConversationMessages", a.GetConversationMessages)
		register("StartPrivateConversation", a.StartPrivateConversation)

		register("GetPeers", a.GetPeers)
		register("GetPeersWithStatus", a.GetPeersWithStatus)
		register("ConnectToPeer", a.ConnectToPeer)
		register("OnForeground", a.OnForeground)

		register("ShareDirectory", a.ShareDirectory)
		register("UnshareDirectory", a.UnshareDirectory)
		register("GetDirectoryChildren", a.GetDirectoryChildren)
		register("GetRemoteDirectoryChildren", a.GetRemoteDirectoryChildren)
		register("StartDownload", a.StartDownload)
		register("GetDefaultDownloadDir", a.GetDefaultDownloadDir)

		register("CreateShareGroup", a.CreateShareGroup)
		register("ListShareGroups", a.ListShareGroups)
		register("AddDirectoryToShareGroup", a.AddDirectoryToShareGroup)
		register("RemoveDirectoryFromShareGroup", a.RemoveDirectoryFromShareGroup)
		register("DeleteShareGroup", a.DeleteShareGroup)
		register("GetShareGroupDirs", a.GetShareGroupDirs)

		register("ExportLogs", a.ExportLogs)
		register("WriteLogFile", a.WriteLogFile)
	})
}

func register(name string, fn interface{}) {
	fv := reflect.ValueOf(fn)
	ft := fv.Type()

	if ft.Kind() != reflect.Func {
		panic(fmt.Sprintf("jsonrpc: %s is not a function", name))
	}

	registeredMethods[name] = func(params json.RawMessage) (interface{}, error) {
		var args []json.RawMessage
		if params != nil && len(params) > 0 {
			if err := json.Unmarshal(params, &args); err != nil {
				return nil, fmt.Errorf("invalid params for %s: %w", name, err)
			}
		}

		numIn := ft.NumIn()
		if len(args) != numIn {
			return nil, fmt.Errorf("%s expects %d arguments, got %d", name, numIn, len(args))
		}

		in := make([]reflect.Value, numIn)
		for i := 0; i < numIn; i++ {
			argType := ft.In(i)
			argVal := reflect.New(argType)
			if err := json.Unmarshal(args[i], argVal.Interface()); err != nil {
				return nil, fmt.Errorf("invalid argument %d for %s: %w", i, name, err)
			}
			in[i] = argVal.Elem()
		}

		results := fv.Call(in)

		var ret interface{}
		var err error

		if len(results) == 1 {
			v := results[0].Interface()
			if v != nil {
				var ok bool
				err, ok = v.(error)
				if !ok {
					ret = v
				}
			}
		} else if len(results) == 2 {
			ret = results[0].Interface()
			if results[1].Interface() != nil {
				err = results[1].Interface().(error)
			}
		}

		if err != nil {
			return nil, err
		}
		return ret, nil
	}
}
