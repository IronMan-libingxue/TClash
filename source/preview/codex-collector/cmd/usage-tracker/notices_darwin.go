//go:build darwin && cgo

package main

/*
#cgo CFLAGS: -fobjc-arc
#cgo LDFLAGS: -framework Foundation -framework UserNotifications
#include <stdlib.h>
void requestUsageNotificationAccess(void);
int usageNotificationAccess(void);
void deliverUsageNotification(const char *title, const char *body);
*/
import "C"
import (
	"unsafe"

	"github.com/yetone/magpie/internal/usagetracker"
)

func nativeNotices() usagetracker.DesktopNotices {
	return usagetracker.DesktopNotices{
		Request: func() { C.requestUsageNotificationAccess() },
		Status: func() string {
			switch C.usageNotificationAccess() {
			case 1:
				return "authorized"
			case 2:
				return "denied"
			case 3:
				return "quiet"
			case 4:
				return "unavailable"
			default:
				return "pending"
			}
		},
		Show: func(title, body string) {
			ct, cb := C.CString(title), C.CString(body)
			defer C.free(unsafe.Pointer(ct))
			defer C.free(unsafe.Pointer(cb))
			C.deliverUsageNotification(ct, cb)
		},
	}
}
