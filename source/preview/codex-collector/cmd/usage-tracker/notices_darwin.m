#import <Foundation/Foundation.h>
#import <UserNotifications/UserNotifications.h>
#include <stdatomic.h>

@interface UsageNotificationDelegate : NSObject <UNUserNotificationCenterDelegate>
@end

@implementation UsageNotificationDelegate
- (void)userNotificationCenter:(UNUserNotificationCenter *)center
        willPresentNotification:(UNNotification *)notification
          withCompletionHandler:(void (^)(UNNotificationPresentationOptions))completionHandler {
    completionHandler(UNNotificationPresentationOptionBanner | UNNotificationPresentationOptionList);
}
@end

static UsageNotificationDelegate *usageDelegate;
static atomic_int accessStatus;
static BOOL requesting;  // Read and written on the main queue.

static void recordUsageSettings(UNNotificationSettings *settings) {
    int status = 0;
    if (settings.authorizationStatus == UNAuthorizationStatusDenied) {
        status = 2;
    } else if (settings.authorizationStatus == UNAuthorizationStatusAuthorized) {
        status = settings.alertSetting == UNNotificationSettingEnabled ? 1 : 3;
    } else if (settings.authorizationStatus == UNAuthorizationStatusProvisional) {
        status = 3;
    }
    atomic_store(&accessStatus, status);
}

void requestUsageNotificationAccess(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        if (NSBundle.mainBundle.bundleIdentifier.length == 0) {
            atomic_store(&accessStatus, 4);
            return;
        }
        if (requesting) {
            return;
        }
        requesting = YES;
        UNUserNotificationCenter *center = UNUserNotificationCenter.currentNotificationCenter;
        if (usageDelegate == nil) {
            usageDelegate = [UsageNotificationDelegate new];
            center.delegate = usageDelegate;
        }
        [center getNotificationSettingsWithCompletionHandler:^(UNNotificationSettings *settings) {
            if (settings.authorizationStatus != UNAuthorizationStatusNotDetermined) {
                recordUsageSettings(settings);
                dispatch_async(dispatch_get_main_queue(), ^{ requesting = NO; });
                return;
            }
            [center requestAuthorizationWithOptions:UNAuthorizationOptionAlert
                                  completionHandler:^(BOOL granted, NSError *error) {
                [center getNotificationSettingsWithCompletionHandler:^(UNNotificationSettings *updated) {
                    recordUsageSettings(updated);
                    dispatch_async(dispatch_get_main_queue(), ^{ requesting = NO; });
                }];
            }];
        }];
    });
}

int usageNotificationAccess(void) {
    return atomic_load(&accessStatus);
}

void deliverUsageNotification(const char *title, const char *body) {
    NSString *notificationTitle = [NSString stringWithUTF8String:title];
    NSString *notificationBody = [NSString stringWithUTF8String:body];
    dispatch_async(dispatch_get_main_queue(), ^{
        int access = atomic_load(&accessStatus);
        if (access != 1 && access != 3) {
            return;
        }
        UNMutableNotificationContent *content = [UNMutableNotificationContent new];
        content.title = notificationTitle;
        content.body = notificationBody;
        UNNotificationRequest *request = [UNNotificationRequest requestWithIdentifier:NSUUID.UUID.UUIDString
                                                                               content:content
                                                                               trigger:nil];
        [UNUserNotificationCenter.currentNotificationCenter addNotificationRequest:request
                                                           withCompletionHandler:^(NSError *error) {
            if (error != nil) {
                NSLog(@"Codex Usage notification could not be delivered (%ld)", (long)error.code);
            }
        }];
    });
}
