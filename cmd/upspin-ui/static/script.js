// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// FormatEntryTime returns the Time for the given DirEntry as a string.
function FormatEntryTime(entry) {
	if (!entry.Time) {
		return "-";
	}
	// TODO(adg): better date formatting.
	return (new Date(entry.Time*1000)).toLocaleString();
}

// FormatEntrySize returns the computed size of the given entry as a string.
function FormatEntrySize(entry) {
	if (!entry.Blocks) {
		return "-";
	}
	var size = 0;
	for (var j=0; j<entry.Blocks.length; j++) {
		size += entry.Blocks[j].Size;
	}
	return ""+size;
}

// FormatEntryAttr returns the Attributes for the given entry as a string.
function FormatEntryAttr(entry) {
	var a = entry.Attr;
	var isDir = a & 1;
	var isLink = a & 2;
	var isIncomplete = a & 4;

	var s = "";
	if (isDir) {
		s = "Directory";
	}
	if (isLink) {
		s = "Link";
	}
	if (isIncomplete) {
		if (s != "") {
			s += ", ";
		}
		s += "Incomplete";
	}
	if (s == "") {
		s = "None";
	}
	return s;
}

// Inspector displays a modal containing the details of the given entity.
function Inspect(entry) {
	var el = $("#mInspector");
	el.find(".up-entry-name").text(entry.Name);
	el.find(".up-entry-size").text(FormatEntrySize(entry));
	el.find(".up-entry-time").text(FormatEntryTime(entry));
	el.find(".up-entry-attr").text(FormatEntryAttr(entry));
	el.find(".up-entry-writer").text(entry.Writer);
	el.modal("show");
}

// Confirm displays a modal that prompts the user to confirm the copy or delete
// of the given paths. If action is "copy", dest should be the copy destination.
// The callback argument is a niladic function that performs the action.
function Confirm(action, paths, dest, callback) {
	var el = $("#mConfirm");

	var button = el.find(".up-confirm-button");
	if (action == "delete") {
		button.addClass("btn-danger");
	} else {
		button.removeClass("btn-danger");
	}
	button.off("click").click(function() {
		el.modal("hide");
		callback();
	});

	el.find(".up-action").text(action);

	var pathsEl = el.find(".up-paths").empty();
	for (var i=0; i<paths.length; i++) {
		pathsEl.append($("<li>").text(paths[i]));
	}

	if (dest) {
		el.find(".up-dest-message").show();
		el.find(".up-dest").text(dest);
	} else {
		el.find(".up-dest-message").hide();
	}

	el.modal("show");
}

// Mkdir displays a modal that prompts the user for a directory to create.
// The basePath is the path to pre-fill in the input box.
// The mkdir argument is a function that creates a directory and takes
// the path name as its single argument.
function Mkdir(basePath, mkdir) {
	var el = $("#mMkdir");
	var input = el.find(".up-path").val(basePath);

	el.find(".up-mkdir-button").off("click").click(function() {
		el.modal("hide");
		mkdir(input.val());
	});

	el.modal("show").on("shown.bs.modal", function() {
		input.focus();
	});
}

// Browser instantiates an Upspin tree browser and appends it to parentEl.
function Browser(parentEl, page) {
	var browser = {
		path: "",
		entries: [],
		navigate: navigate,
		refresh: refresh,
		reportError: reportError
	};

	var el = $("body > .up-template.up-browser").clone().removeClass("up-template");
	el.appendTo(parentEl);

	function navigate(path) {
		browser.path = path;
		drawPath();
		drawLoading("Loading directory...");
		page.list(path, function(entries) {
			drawEntries(entries);
		}, function(error) {
			reportError(error);
		});
	}

	function refresh() {
		navigate(browser.path);
	}

	el.on("dragover", function(e) {
		e.preventDefault();
		el.addClass("drag");
	});
	el.on("dragleave", function(e) {
		e.preventDefault();
		el.removeClass("drag");
	});
	el.on("drop", function(e) {
		e.preventDefault();
		el.removeClass("drag");

		if (!e.originalEvent.dataTransfer || e.originalEvent.dataTransfer.files.length == 0) {
			return;
		}

		drawLoading("Uploading files...");

		var files = e.originalEvent.dataTransfer.files;
		page.put(browser.path, files, function() {
			inputs.attr("disabled", false);
			refresh();
		}, function(err) {
			inputs.attr("disabled", false);
			reportError(err);
		});
	});

	el.find(".up-delete").click(function() {
		var paths = checkedPaths();
		if (paths.length == 0) {
			return;
		}
		Confirm("delete", paths, null, function() {
			page.rm(paths, function() {
				refresh();
			}, function(err) {
				reportError(err);
				// Refresh the pane because entries may have
				// been deleted even if an error occurred.
				refresh();
			});
		});
	});

	el.find(".up-copy").click(function() {
		var paths = checkedPaths();
		if (paths.length == 0) {
			return;
		}
		var dest = page.copyDestination();
		Confirm("copy", paths, dest, function() {
			page.copy(paths, dest, function() {
				page.refreshDestination();
			}, function(error) {
				reportError(error);
				// Refresh the destination pane as files may
				// have been copied even if an error occurred.
				page.refreshDestination();
			});
		});
	});

	el.find(".up-refresh").click(function() {
		refresh();
	});

	el.find(".up-mkdir").click(function() {
		Mkdir(browser.path+"/", function(path) {
			page.mkdir(path, function() {
				refresh();
			}, function(error) {
				reportError(error);
			});
		});
	});

	el.find(".up-select-all").on("change", function() {
		var checked = $(this).is(":checked");
		el.find(".up-entry").not(".up-template").find(".up-entry-select").each(function() {
			$(this).prop("checked", checked);
		});
	});

	function checkedPaths() {
		var paths = [];
		el.find(".up-entry").not(".up-template").each(function() {
			var checked = $(this).find(".up-entry-select").is(":checked");
			if (checked) {
				paths.push($(this).data("up-entry").Name);
			}
		});
		return paths;
	}

	function atRoot() {
		var p = browser.path;
		var i = p.indexOf("/");
		return i == -1 || i == p.length-1;
	}

	var parentEl = el.find(".up-parent").click(function() {
		if (atRoot()) return;

		var p = browser.path;
		var i = p.lastIndexOf("/");
		navigate(p.slice(0, i));
	});

	var pathEl = el.find(".up-path").change(function() {
		navigate($(this).val());
	});

	function drawPath() {
		var p = browser.path;
		pathEl.val(p);

		var i = p.indexOf("/")
		parentEl.prop("disabled", atRoot());
	}

	var loadingEl = el.find(".up-loading"),
		errorEl = el.find(".up-error"),
		entriesEl = el.find(".up-entries"),
		inputs = el.find("button, input");

	function drawLoading(text) {
		inputs.attr("disabled", true);
		loadingEl.show().text(text);
		errorEl.hide();
		entriesEl.hide();
	}

	function reportError(err) {
		inputs.attr("disabled", false);
		loadingEl.hide();
		errorEl.show().text(err);
	}

	function drawEntries(entries) {
		inputs.attr("disabled", false);
		loadingEl.hide();
		errorEl.hide();
		entriesEl.show();

		el.find(".up-select-all").prop("checked", false);

		var tmpl = el.find(".up-template.up-entry");
		var parent = tmpl.parent();
		parent.children().filter(".up-entry").not(tmpl).remove();
		for (var i=0; i<entries.length; i++) {
			var entry = entries[i];
			var entryEl = tmpl.clone().removeClass("up-template");
			entryEl.data("up-entry", entry);

			var isDir = entry.Attr & 1;
			var isLink = entry.Attr & 2;

			var glyph = "file";
			if (isDir) {
				glyph = "folder-close";
			} else if (isLink) {
				glyph = "share-alt";
			}
			entryEl.find(".up-entry-icon").addClass("glyphicon-"+glyph);

			var name = entry.Name;
			var shortName = name.slice(name.lastIndexOf("/")+1);
			var nameEl = entryEl.find(".up-entry-name");
			if (isDir) {
				nameEl
					.text(shortName)
					.addClass("up-clickable")
					.data("up-path", name)
					.click(function(event) {
						var p = $(this).data("up-path");
						navigate(p);
					});
			} else {
				$("<a>")
					.text(shortName)
					.attr("href", "/" + name + "?token=" + entry.FileToken)
					.attr("target", "_blank")
					.appendTo(nameEl);
			}

			var sizeEl = entryEl.find(".up-entry-size");
			if (isDir) {
				sizeEl.text("-");
			} else{
				sizeEl.text(FormatEntrySize(entry));
			}

			entryEl.find(".up-entry-time").text(FormatEntryTime(entry));

			var inspectEl = entryEl.find(".up-entry-inspect");
			inspectEl.data("up-entry", entry);
			inspectEl.click(function() {
				Inspect($(this).closest(".up-entry").data("up-entry"));
			});

			parent.append(entryEl);
		}
		var emptyEl = parent.find(".up-empty");
		if (entries.length == 0) {
			emptyEl.show();
		} else {
			emptyEl.hide();
		}
	}

	return browser;
}

// Startup manages the signup process and fetches the name of the logged-in
// user and the XSRF token for making subsequent requests.
function Startup(xhr, doneCallback) {

	$("#mSignup").find("button").click(function() {
		action({
			action: "signup",
			username: $("#signupUserName").val(),
		});
	});

	$("#mSecretSeed").find("button").click(function() {
		action();
	});

	$("#mVerify").find("button.up-resend").click(function() {
		action({action: "register"});
	});
	$("#mVerify").find("button.up-proceed").click(function() {
		action();
	});

	$("#mServerSelect").find("button").click(function() {
		switch (true) {
		case $("#serverSelectExisting").is(":checked"):
			show({Step: "serverExisting"});
			break;
		case $("#serverSelectGCP").is(":checked"):
			show({Step: "serverGCP"});
			break;
		case $("#serverSelectNone").is(":checked"):
			action({action: "specifyNoEndpoints"});
			break;
		}
	});

	$("#mServerExisting").find("button").click(function() {
		action({
			action: "specifyEndpoints",
			dirServer: $("#serverExistingDirServer").val(),
			storeServer: $("#serverExistingStoreServer").val()
		});
	});

	$("#mServerGCP").find("button").click(function() {
		var fileEl = $("#serverGCPKeyFile");
		if (fileEl[0].files.length != 1) {
			error("You must provide a JSON Private Key file.");
			return;
		}
		var r = new FileReader();
		r.onerror = function() {
			error("An error occurred uploading the file.");
		};
		r.onload = function(state) {
			action({
				action: "specifyGCP",
				privateKeyData: r.result
			});
		};
		r.readAsText(fileEl[0].files[0]);
	});

	$("#mGCPDetails").find("button").click(function() {
		action({
			action: "createGCP",
			bucketName: $("#gcpDetailsBucketName").val(),
			bucketLoc: $("#gcpDetailsBucketLoc").val(),
			regionZone: $("#gcpDetailsRegionZone").val()
		});
	});

	$("#mServerUserName").find("button").click(function() {
		action({
			action: "configureServerUserName",
			userNameSuffix: $("#serverUserNameSuffix").val()
		});
	});

	$("#mServerSecretSeed").find("button").click(function() {
		// Performing an empty action will bounce the user to the next
		// screen, serverHostName, with the server IP address populated
		// by the server side.
		action({});
	});

	$("#mServerHostName").find("button").click(function() {
		action({
			action: "configureServerHostName",
			hostName: $("#serverHostName").val()
		});
	});

	$("#mWaitServerHostName").find("button.btn-primary").click(function() {
		action({action: "checkServerHostName"});
	});
	$("#mWaitServerHostName").find("button.btn-danger").click(function() {
		action({
			action: "checkServerHostName",
			reset: "true"
		});
	});

	$("#mServerWriters").find("button").click(function() {
		action({
			action: "configureServer",
			writers: $("#serverWriters").val()
		});
	});

	var step; // String representation of the current step.
	var el; // jQuery element of the current step's modal.
	function show(data) {
		// If we've moved onto another step, hide the previous one.
		if (el && data.Step != step) {
			el.modal("hide");
		}

		// Set el and step and do step-specific setup.
		switch (data.Step) {
		case "loading":
			el = $("#mLoading");
			break;
		case "signup":
			el = $("#mSignup");
			break;
		case "secretSeed":
			el = $("#mSecretSeed");
			$("#secretSeedKeyDir").text(data.KeyDir);
			$("#secretSeedSecretSeed").text(data.SecretSeed);
			break;
		case "verify":
			el = $("#mVerify");
			el.find(".up-username").text(data.UserName);
			break;
		case "serverSelect":
			el = $("#mServerSelect");
			break;
		case "serverExisting":
			el = $("#mServerExisting");
			break;
		case "serverGCP":
			el = $("#mServerGCP");
			break;
		case "gcpDetails":
			el = $("#mGCPDetails");

			$("#gcpDetailsBucketName").val(data.BucketName);

			var locs = $("#gcpDetailsBucketLoc").empty();
			for (var i=0; i < data.Locations.length; i++) {
				var loc = data.Locations[i];
				var label = loc;
				if (loc.indexOf("-") >= 0) {
					label += " (Regional)";
				} else {
					label += " (Multi-regional)";
				}
				var opt = $("<option/>").attr("value", loc).text(label);
				if (loc == "us") {
					opt.attr("selected", true); // A sane default.
				}
				locs.append(opt);
			}

			var zones = $("#gcpDetailsRegionZone").empty();
			for (var i=0; i < data.Zones.length; i++) {
				var zone = data.Zones[i];
				var label = zone.slice(zone.indexOf("/")+1);
				var opt = $("<option/>").attr("value", zone).text(label);
				if (zone == "us-central1/us-central1-c") {
					opt.attr("selected", true); // As sane default.
				}
				zones.append(opt);
			}

			break;
		case "serverUserName":
			el = $("#mServerUserName");
			$("#serverUserNamePrefix").text(data.UserNamePrefix);
			$("#serverUserNameSuffix").val(data.UserNameSuffix);
			$("#serverUserNameDomain").text(data.UserNameDomain);
			break;
		case "serverSecretSeed":
			el = $("#mServerSecretSeed");
			$("#serverSecretSeedKeyDir").text(data.KeyDir);
			$("#serverSecretSeedSecretSeed").text(data.SecretSeed);
			break;
		case "serverHostName":
			el = $("#mServerHostName");
			el.find(".up-ipAddr").text(data.IPAddr);
			break;
		case "waitServerHostName":
			el = $("#mWaitServerHostName");
			el.find(".up-ipAddr").text(data.IPAddr);
			el.find(".up-hostName").text(data.HostName);
			if (data.HostName.endsWith("upspin.services")) {
				el.find(".up-upspinServices").show();
				el.find(".up-ownDomain").hide();
			} else {
				el.find(".up-upspinServices").hide();
				el.find(".up-ownDomain").show();
			}
			break;
		case "serverWriters":
			el = $("#mServerWriters");
			$("#serverWriters").val(data.Writers.join("\n"));
			break;
		}
		step = data.Step;

		// Enable buttons, create spinners (or stop existing ones),
		// hide old errors, show the dialog.
		el.find("button").each(function() {
			var l = $(this).data("Ladda");
			if (typeof l == "object") {
				l.stop();
				return;
			}
			l = Ladda.create(this);
			$(this).data("Ladda", l).click(function() {
				l.start();
			});
		});
		el.find("button, input, select").prop("disabled", false);
		el.find(".up-error").hide();
		el.modal("show");
	}
	function success(resp) {
		if (!resp.Startup) {
			// The startup process is complete.
			if (el) {
				el.modal("hide");
			}
			doneCallback(resp);
			return;
		}
		show(resp.Startup);
	}
	function error(err) {
		if (el) {
			// Show the error, re-enable buttons.
			el.find(".up-error").show().find(".up-error-msg").text(err);
			el.find("button, input").prop("disabled", false);
			el.find("button").each(function() {
				var l = $(this).data("Ladda");
				if (typeof l == "object") {
					l.stop();
				}
			});
		} else {
			alert(err)
			// TODO(adg): display the initial error in a more friendly way.
		}
	}
	function action(data) {
		if (el) {
			// Disable buttons, hide old errors.
			el.find("button, input").prop("disabled", true);
			el.find(".up-error").hide();
		}
		xhr(data, success, error);
	}

	show({Step: "loading"});
	action(); // Kick things off.
}

function Page() {
	var page = {
		username: "",
		key: ""
	};

	// errorHandler returns an XHR error callback that invokes the given
	// browser error callback with the human-readable error string.
	function errorHandler(callback) {
		return function(jqXHR, textStatus, errorThrown) {
			console.log(textStatus, errorThrown);
			if (errorThrown) {
				callback(errorThrown);
				return;
			}
			callback(textStatus);
		}
	}

	function list(path, success, error) {
		$.ajax("/_upspin", {
			method: "POST",
			data: {
				key: page.key,
				method: "list",
				path: path,
			},
			dataType: "json",
			success: function(data) {
				if (data.Error) {
					error(data.Error);
					return;
				}
				success(data.Entries);
			},
			error: errorHandler(error)
		});
	}

	function rm(paths, success, error) {
		$.ajax("/_upspin", {
			method: "POST",
			data: {
				key: page.key,
				method: "rm",
				paths: paths
			},
			dataType: "json",
			success: function(data) {
				if (data.Error) {
					error(data.Error);
					return;
				}
				success();
			},
			error: errorHandler(error)
		});
	}

	function copy(paths, dest, success, error) {
		$.ajax("/_upspin", {
			method: "POST",
			data: {
				key: page.key,
				method: "copy",
				paths: paths,
				dest: dest
			},
			dataType: "json",
			success: function(data) {
				if (data.Error) {
					error(data.Error);
					return;
				}
				success();
			},
			error: errorHandler(error)
		});
	}

	function mkdir(path, success, error) {
		$.ajax("/_upspin", {
			method: "POST",
			data: {
				key: page.key,
				method: "mkdir",
				path: path
			},
			dataType: "json",
			success: function(data) {
				if (data.Error) {
					error(data.Error);
					return;
				}
				success();
			},
			error: errorHandler(error)
		});
	}

	function put(dir, files, success, error) {
		// For the file upload to work, we need to pass the files in as
		// a FormData object and turn off any of the pre-processing
		// jQuery might do.
		var fd = new FormData();
		fd.append("key", page.key);
		fd.append("method", "put");
		fd.append("dir", dir);
		for (var i = 0; i < files.length; i++) {
			fd.append("file"+i, files[i]);
		}
		$.ajax("/_upspin", {
			method: "POST",
			data: fd,
			contentType: false,
			processData: false,
			cache: false,
			dataType: "json",
			success: function(data) {
				if (data.Error) {
					error(data.Error);
					return;
				}
				success();
			},
			error: errorHandler(error)
		});
	}

	function startup(data, success, error) {
		$.ajax("/_upspin", {
			method: "POST",
			data: $.extend({
				key: page.key,
				method: "startup"
			}, data),
			dataType: "json",
			success: function(data) {
				if (data.Error) {
					error(data.Error);
					return;
				}
				success(data);
			},
			error: errorHandler(error)
		});
	}

	function startBrowsers(leftPath, rightPath) {
		var browser1, browser2;
		var parentEl = $(".up-browser-parent");
		var methods = {
			rm: rm,
			copy: copy,
			list: list,
			mkdir: mkdir,
			put: put
		}
		browser1 = new Browser(parentEl, $.extend({
			copyDestination: function() { return browser2.path },
			refreshDestination: function() { browser2.refresh(); }
		}, methods));
		browser2 = new Browser(parentEl, $.extend({
			copyDestination: function() { return browser1.path },
			refreshDestination: function() { browser1.refresh(); }
		}, methods));
		browser1.navigate(leftPath);
		browser2.navigate(rightPath);
	}

	// Obtain a request key.
	var prefix = "#key="
	if (!window.location.hash.startsWith(prefix)) {
		$("#mLoading").modal("show").find(".up-error")
			.show().find(".up-error-msg")
			.text("No request key in browser URL.\n\n" +
				"To use the Upspin browser, click the URL\n" +
				"that it printed to the console.\n\n" +
				"It will look something like\n" +
				" http://localhost:8000/#key=3f0cf1e29...\n" +
				"but with a different hash.");
		return;
	}
	page.key = window.location.hash.slice(prefix.length);
	window.location.hash = "";

	// Begin the Startup sequence.
	Startup(startup, function(data) {
		// When startup is complete, note the
		// user name and launch the browsers.
		page.username = data.UserName;
		$("#headerUsername").text(page.username);
		$("#headerVersion").text(data.Version);
		startBrowsers(data.LeftPath, data.RightPath);
	});
}

// Start everything.
new Page();
