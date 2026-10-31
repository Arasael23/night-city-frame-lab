// Night City Frame Lab - session markers
//
// Frame Lab puts this file into the game for one recording session and takes it out again
// afterwards. It writes one line to CET's gamelog.log when a loading screen starts or ends and
// when V enters or leaves the game world, so the report can leave loading screens and the main
// menu out of the stutter list. It changes nothing in the game.

module NightCityFrameLab

public abstract class FrameLabMark {
  public static func Say(text: String) -> Void {
    FTLog("[Frame Lab] " + text);
  }
}

@wrapMethod(LoadingScreenProgressBarController)
protected cb func OnInitialize() -> Bool {
  FrameLabMark.Say("loading screen on");
  return wrappedMethod();
}

@addMethod(LoadingScreenProgressBarController)
protected cb func OnUninitialize() -> Bool {
  FrameLabMark.Say("loading screen off");
  return true;
}

@wrapMethod(PlayerPuppet)
protected cb func OnGameAttached() -> Bool {
  let r: Bool = wrappedMethod();
  FrameLabMark.Say("in game");
  return r;
}

@wrapMethod(PlayerPuppet)
protected cb func OnDetach() -> Bool {
  FrameLabMark.Say("left the game");
  return wrappedMethod();
}
