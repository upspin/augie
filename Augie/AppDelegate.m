// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#import "AppDelegate.h"

@interface AppDelegate()
@property(nonatomic, strong) NSStatusItem* statusItem;
@end

@implementation AppDelegate

- (void)applicationDidFinishLaunching:(NSNotification*)aNotification {
  self.statusItem = [[NSStatusBar systemStatusBar] statusItemWithLength:NSVariableStatusItemLength];
  self.statusItem.image = [NSImage imageNamed:@"augie-menubar"];

  NSMenu* menu = [NSMenu new];
  [menu addItem:[[NSMenuItem alloc] initWithTitle:@"Quit"
                                           action:@selector(terminate:)
                                    keyEquivalent:@""]];
  self.statusItem.menu = menu;
}

@end
